packer {
  required_plugins {
    azure = {
      source  = "github.com/hashicorp/azure"
      version = "~> 2"
    }
  }
}

variable "tenant_id" {
  type = string
}

variable "subscription_id" {
  type = string
}

variable "vm_size" {
  type    = string
  default = "Standard_D2s_v5"
}

variable "github_actions_version" {
  type    = string
  default = "2.338.1"
}

variable "image_resource_group" {
  type    = string
  default = "gh-actions-images-eastus2"
}

# Existing resource group (managed by Terraform) for Packer's temporary VM, network
# and Key Vault. The build location is taken from this resource group.
variable "build_resource_group" {
  type    = string
  default = "gh-actions-image-build-eastus2"
}

# Source addresses allowed to reach WinRM on the build VM. Empty keeps the plugin
# default (open); CI passes the GitHub runner's public IP.
variable "allowed_inbound_ip_addresses" {
  type    = list(string)
  default = []
}

# Set in CI to authenticate with GitHub OIDC. The plugin requests fresh tokens itself,
# so builds longer than the az CLI token lifetime keep working. Empty uses the local
# az CLI login.
variable "client_id" {
  type    = string
  default = ""
}

variable "oidc_request_url" {
  type    = string
  default = env("ACTIONS_ID_TOKEN_REQUEST_URL")
}

variable "oidc_request_token" {
  type      = string
  default   = env("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
  sensitive = true
}

variable "virtualization_flavor" {
  type    = string
  default = "hyperv"
  validation {
    condition     = contains(["hyperv", "virtualbox"], var.virtualization_flavor)
    error_message = "Virtualization_flavor must be either 'hyperv' or 'virtualbox'."
  }
}

locals {
  bootstrap_script      = <<EOF
$bootstrap = @'
Set-Content -Path 'C:\AzureData\provision_log.txt' -Value 'Starting custom data execution...'
try {
    if (Test-Path 'C:\AzureData\CustomData.bin') {
        $scriptPath = 'C:\AzureData\CustomData.ps1'
        Copy-Item 'C:\AzureData\CustomData.bin' $scriptPath -Force
        powershell.exe -ExecutionPolicy Bypass -File $scriptPath 2>&1 | Out-File -FilePath 'C:\AzureData\provision_log.txt' -Append -Encoding utf8
        Add-Content -Path 'C:\AzureData\provision_log.txt' -Value 'Execution successful.'
    } else {
        Add-Content -Path 'C:\AzureData\provision_log.txt' -Value 'Error: CustomData.bin not found.'
    }
} catch {
    $_.Exception.Message | Out-File -FilePath 'C:\AzureData\provision_log.txt' -Append
}
'@
Set-Content -Path 'C:\AzureData\bootstrap.ps1' -Value $bootstrap
EOF
  windows_setup_command = var.virtualization_flavor == "virtualbox" ? "powershell.exe -ExecutionPolicy Bypass -File C:\\AzureData\\scripts\\windows\\sailwright-self-setup.ps1 -WithGo -VirtualBox" : "powershell.exe -ExecutionPolicy Bypass -File C:\\AzureData\\scripts\\windows\\sailwright-self-setup.ps1 -WithGo"
  refresh_path_command  = "$env:Path = @([Environment]::GetEnvironmentVariable('Path', 'Machine'), [Environment]::GetEnvironmentVariable('Path', 'User')) -join ';'"

}


source "azure-arm" "windows-azure-gh-runner" {
  azure_tags = {
    task = "Image deployment"
  }
  build_resource_group_name         = var.build_resource_group
  allowed_inbound_ip_addresses      = var.allowed_inbound_ip_addresses
  use_azure_cli_auth                = var.client_id == ""
  client_id                         = var.client_id
  oidc_request_url                  = var.client_id == "" ? "" : var.oidc_request_url
  oidc_request_token                = var.client_id == "" ? "" : var.oidc_request_token
  communicator                      = "winrm"
  image_offer                       = "WindowsServer"
  image_publisher                   = "MicrosoftWindowsServer"
  image_sku                         = "2022-datacenter-g2"
  managed_image_name                = "Win2022GHAzureRunner-${var.virtualization_flavor}"
  managed_image_resource_group_name = var.image_resource_group
  os_type                           = "Windows"
  tenant_id                         = var.tenant_id
  subscription_id                   = var.subscription_id
  vm_size                           = var.vm_size
  winrm_insecure                    = true
  winrm_timeout                     = "5m"
  winrm_use_ssl                     = true
  winrm_username                    = "packer"

  # Stored images are billed per used GB like snapshots. The image is only read when a
  # runner VM is created, so Standard HDD is enough; the VM OS disk type is set
  # independently by the runner broker (scripts/gh-runner-func/function_app.py).
  managed_image_storage_account_type = "Standard_LRS"

  spot {
    eviction_policy = "Delete"
    max_price       = -1
  }
}

build {
  sources = ["source.azure-arm.windows-azure-gh-runner"]

  provisioner "file" {
    source      = "../../scripts/windows/install_oscdimg.ps1"
    destination = "C:\\AzureData\\scripts\\windows\\install_oscdimg.ps1"
  }

  provisioner "file" {
    source      = "../../scripts/windows/sailwright-self-setup.ps1"
    destination = "C:\\AzureData\\scripts\\windows\\sailwright-self-setup.ps1"
  }

  provisioner "powershell" {
    inline = [
      # Wait for the Azure Guest Agent to be running
      "while ((Get-Service RdAgent).Status -ne 'Running') { Start-Sleep -s 5 }",
      "while ((Get-Service WindowsAzureGuestAgent).Status -ne 'Running') { Start-Sleep -s 5 }",

      # Install oscdimg for image capture
      "powershell.exe -ExecutionPolicy Bypass -File C:\\AzureData\\scripts\\windows\\install_oscdimg.ps1",

      # Github Actions Runner
      "Invoke-WebRequest -Uri \"https://github.com/actions/runner/releases/download/v${var.github_actions_version}/actions-runner-win-x64-${var.github_actions_version}.zip\" -OutFile \"C:\\actions-runner.zip\"",
      "Expand-Archive \"C:\\actions-runner.zip\" -DestinationPath \"C:\\actions-runner\"",
      "Remove-Item \"C:\\actions-runner.zip\"",

      # install host build packages via the shared windows setup script
      local.windows_setup_command,
      # The setup script runs in a child PowerShell process, so refresh this provisioner's PATH before verifying installed tools.
      local.refresh_path_command,
      "python --version",
      "go version",
      # loader script to execute custom data on first boot
      "New-Item -Path 'C:\\AzureData' -ItemType Directory -Force",
      local.bootstrap_script,

      ## Create a Scheduled Task to run this at every boot
      "$action = New-ScheduledTaskAction -Execute 'PowerShell.exe' -Argument '-ExecutionPolicy Bypass -File C:\\AzureData\\bootstrap.ps1'",
      "$trigger = New-ScheduledTaskTrigger -AtStartup",
      "$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest",
      "Register-ScheduledTask -TaskName 'AzureProvisioning' -Action $action -Trigger $trigger -Principal $principal",

      # generalize the image
      "while ((Get-Service RdAgent).Status -ne 'Running') { Start-Sleep -s 5 }",
      "while ((Get-Service WindowsAzureGuestAgent).Status -ne 'Running') { Start-Sleep -s 5 }",
      "& $env:SystemRoot\\System32\\Sysprep\\Sysprep.exe /oobe /generalize /quiet /quit",
      "while($true) { $imageState = Get-ItemProperty HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Setup\\State | Select ImageState; if($imageState.ImageState -ne 'IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE') { Write-Output $imageState.ImageState; Start-Sleep -s 10  } else { break } }"
    ]
  }
}
