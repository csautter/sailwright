BeforeAll {
    # The broker substitutes these values before the image runs CustomData.ps1.
    $template = Get-Content -Raw (Join-Path $PSScriptRoot "../../scripts/gh-runner-func/runner-setup.ps1")
    $payload = $template.Replace("__VIRTUALIZATION_FLAVOR__", "hyperv").Replace("__RUNNER_TOKEN__", "test-token").Replace("__REPO_URL__", "https://github.com/example/repo")
    $payloadPath = Join-Path $TestDrive "CustomData.ps1"
    Set-Content -Path $payloadPath -Value $payload -Encoding UTF8

    function New-TestIPv4 {
        param(
            [int]$InterfaceIndex = 24,
            [string]$IPAddress = "192.168.0.1",
            [int]$PrefixLength = 24,
            [string]$AddressState = "Preferred",
            [bool]$SkipAsSource = $false
        )
        [pscustomobject]@{
            InterfaceIndex = $InterfaceIndex
            IPAddress = $IPAddress
            PrefixLength = $PrefixLength
            AddressState = $AddressState
            SkipAsSource = $SkipAsSource
        }
    }

    function Invoke-TestRunnerSetup {
        [Diagnostics.CodeAnalysis.SuppressMessageAttribute("PSAvoidOverwritingBuiltInCmdlets", "", Justification = "Test doubles shadow host commands in this function scope.")]
        [Diagnostics.CodeAnalysis.SuppressMessageAttribute("PSReviewUnusedParameter", "Fixture", Justification = "The test doubles share the fixture with the caller.")]
        param([hashtable]$Fixture)

        # These functions shadow host commands only for the provisioner invocation.
        function Join-Path { param($Path, $ChildPath) "$Path\$ChildPath" }
        function New-Item {}
        function Add-Content {}
        function Set-Content {}
        function Test-Path { $true }
        function Set-Location {}
        function Set-Acl {}
        function Import-Module {}
        function Unregister-ScheduledTask {}
        function Get-CimInstance {
            switch ($args[0]) {
                "Win32_OperatingSystem" { [pscustomobject]@{ Caption = "Windows Server 2022" } }
                "Win32_ComputerSystem" { [pscustomobject]@{ PartOfDomain = $false } }
            }
        }
        function Get-WindowsFeature { [pscustomobject]@{ Installed = $true } }
        function Get-VMSwitch { [pscustomobject]@{ Name = "Default Switch" } }
        function Get-NetNat { $true }
        function Get-DhcpServerv4Scope { $true }
        function Get-DhcpServerv4Binding {}
        function Set-DhcpServerv4Binding {}
        function Add-Type {}
        function Get-LocalUser { $true }
        function Add-LocalGroupMember {}
        function Get-Service {}
        function Start-Sleep {
            if ($args[0] -eq "-Milliseconds") {
                $Fixture.Polls++
            }
        }
        function Get-VMNetworkAdapter {
            [CmdletBinding()]
            param([switch]$ManagementOS)

            if (-not $ManagementOS) {
                throw "Expected a management OS adapter lookup."
            }
            [pscustomobject]@{ SwitchName = "Default Switch"; DeviceId = "switch-host-device" }
        }
        function Get-NetAdapter {
            # The expected alias on a different device must never satisfy the gate.
            [pscustomobject]@{ Name = "vEthernet (Default Switch)"; DeviceId = "other-device"; InterfaceIndex = 99 }
            if ($Fixture.Polls -ge $Fixture.AdapterReadyAfterPolls) {
                [pscustomobject]@{ Name = "Renamed switch adapter"; DeviceId = "switch-host-device"; InterfaceIndex = 24 }
            }
        }
        function Get-NetIPAddress {
            [CmdletBinding()]
            param([string]$AddressFamily, [string]$PolicyStore)

            if ($AddressFamily -eq "IPv4" -and $PolicyStore -eq "ActiveStore") {
                $Fixture.AddressReads++
                if ($Fixture.ReadError) {
                    Write-Error $Fixture.ReadError
                }
                & $Fixture.ReadAddresses
            }
        }
        function New-NetIPAddress {
            [CmdletBinding()]
            param([string]$IPAddress, [int]$PrefixLength, [int]$InterfaceIndex)

            $Fixture.Assignments += "$IPAddress/$PrefixLength on index $InterfaceIndex"
            if ($Fixture.AssignmentError) {
                Write-Error $Fixture.AssignmentError
            }
            # A successful cmdlet result still requires a separate readback.
            New-TestIPv4
        }
        function .\config.cmd { $Fixture.Registrations++ }

        & $payloadPath 3>$null 6>$null
    }
}

Describe "Hyper-V runner setup IPv4 gate" {
    BeforeEach {
        $script:fixture = @{
            AdapterReadyAfterPolls = 0
            Polls = 0
            AddressReads = 0
            ReadAddresses = { New-TestIPv4 }
            ReadError = $null
            AssignmentError = $null
            Assignments = @()
            Registrations = 0
        }
    }

    It "registers on repeat runs with the ready address on the identified adapter" {
        Invoke-TestRunnerSetup $fixture
        Invoke-TestRunnerSetup $fixture

        $fixture.Registrations | Should -Be 2
        $fixture.AddressReads | Should -Be 4
        $fixture.Polls | Should -Be 0
        $fixture.Assignments | Should -BeNullOrEmpty
    }

    It "waits for the Windows adapter to appear" {
        $fixture.AdapterReadyAfterPolls = 2

        Invoke-TestRunnerSetup $fixture

        $fixture.Polls | Should -Be 2
        $fixture.Registrations | Should -Be 1
        $fixture.Assignments | Should -BeNullOrEmpty
    }

    It "assigns once by index and waits for a separate preferred readback" {
        $fixture.ReadAddresses = {
            if ($Fixture.AddressReads -eq 2) {
                New-TestIPv4 -AddressState Tentative
            }
            if ($Fixture.AddressReads -ge 3) {
                New-TestIPv4
            }
        }

        Invoke-TestRunnerSetup $fixture

        $fixture.Assignments | Should -Be "192.168.0.1/24 on index 24"
        $fixture.Polls | Should -Be 2
        $fixture.Registrations | Should -Be 1
    }

    It "times out before registration if the matching adapter never appears" {
        $fixture.AdapterReadyAfterPolls = 100

        { Invoke-TestRunnerSetup $fixture } |
            Should -Throw "*Default Switch*management OS adapter*192.168.0.1/24*timed out during discover host adapter*"
        $fixture.Polls | Should -Be 29
        $fixture.AddressReads | Should -Be 0
        $fixture.Assignments | Should -BeNullOrEmpty
        $fixture.Registrations | Should -Be 0
    }

    It "blocks registration when IPv4 assignment fails" {
        $fixture.ReadAddresses = {}
        $fixture.AssignmentError = "assignment denied"

        { Invoke-TestRunnerSetup $fixture } |
            Should -Throw "*Default Switch*Renamed switch adapter*index 24*192.168.0.1/24*failed to assign IPv4*assignment denied*"
        $fixture.Assignments | Should -Be "192.168.0.1/24 on index 24"
        $fixture.Registrations | Should -Be 0
    }

    It "blocks registration when active IPv4 cannot be read" {
        $fixture.ReadError = "address provider unavailable"

        { Invoke-TestRunnerSetup $fixture } |
            Should -Throw "*Default Switch*Renamed switch adapter*index 24*192.168.0.1/24*failed to read active IPv4*address provider unavailable*"
        $fixture.Assignments | Should -BeNullOrEmpty
        $fixture.Registrations | Should -Be 0
    }

    It "rejects an unusable address with <Reason>" -ForEach @(
        @{ Reason = "a wrong prefix"; Address = @{ PrefixLength = 16 }; Observation = "Found 192.168.0.1/16"; Assigned = $false }
        @{ Reason = "a duplicate state"; Address = @{ AddressState = "Duplicate" }; Observation = "state Duplicate"; Assigned = $false }
        @{ Reason = "SkipAsSource"; Address = @{ SkipAsSource = $true }; Observation = "SkipAsSource True"; Assigned = $false }
        @{ Reason = "the expected IP on another interface"; Address = @{ InterfaceIndex = 99 }; Observation = "absent on this interface"; Assigned = $true }
        @{ Reason = "a different IPv4 on the right interface"; Address = @{ IPAddress = "192.168.0.2" }; Observation = "absent on this interface"; Assigned = $true }
    ) {
        $fixture.AddressOptions = $Address
        $fixture.ReadAddresses = {
            $options = $Fixture.AddressOptions
            New-TestIPv4 @options
        }

        { Invoke-TestRunnerSetup $fixture } |
            Should -Throw "*index 24*192.168.0.1/24*timed out during read active IPv4*$Observation*"
        $fixture.Assignments.Count | Should -Be ([int]$Assigned)
        $fixture.Registrations | Should -Be 0
    }

    It "checks again before registration when the initially ready address disappears" {
        $fixture.ReadAddresses = {
            if ($Fixture.AddressReads -eq 1) {
                New-TestIPv4
            }
        }

        { Invoke-TestRunnerSetup $fixture } |
            Should -Throw "*index 24*192.168.0.1/24*timed out during read active IPv4*absent on this interface*"
        $fixture.AddressReads | Should -Be 31
        $fixture.Assignments | Should -BeNullOrEmpty
        $fixture.Registrations | Should -Be 0
    }
}
