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
  default = "2.337.0"
}

variable "image_resource_group" {
  type    = string
  default = "gh-actions-images"
}

variable "location" {
  type    = string
  default = "East US"
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
  temp_resource_group_name          = "packerResourceGroup_temp"
  location                          = var.location
  use_azure_cli_auth                = true
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

  managed_image_storage_account_type = "Premium_LRS" # Premium SSD

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
