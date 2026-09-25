packer {
  required_version = ">= 1.12.0"
  required_plugins {
    virtualbox = {
      version = ">= 1.1.5"
      source  = "github.com/hashicorp/virtualbox"
    }
    vagrant = {
      version = ">= 1.1.7"
      source  = "github.com/hashicorp/vagrant"
    }
  }
}

variable "iso_url" {
  type    = string
  default = ""
}

variable "nested_virt" {
  type        = bool
  default     = false
  description = "Do NOT enable on Azure/Hyper-V cloud machines - Hyper-V owns VT-x, causing VirtualBox to software-emulate nested virt at extreme CPU cost"
}

variable "cpus" {
  type    = number
  default = 2
}

variable "memory" {
  type        = number
  default     = 4096
  description = "Memory in MB to allocate to the VM"
}

variable "temp_disk_path" {
  type        = string
  default     = ""
  description = "Path to use for temporary files and VM storage (e.g., D:\\ for Azure local temp disk)"
}

variable "cache_dir" {
  type        = string
  default     = env("DEV_ALCHEMY_CACHE_DIR")
  description = "Managed cache directory outside the repository."
  validation {
    condition     = var.cache_dir != ""
    error_message = "The cache_dir variable must be set, typically via DEV_ALCHEMY_CACHE_DIR."
  }
}

variable "artifact_output_path" {
  type        = string
  default     = ""
  description = "Optional Vagrant box artifact path. Used to stage no-cache rebuilds before promotion."
}

locals {
  box_output = var.artifact_output_path != "" ? var.artifact_output_path : "${var.cache_dir}/windows11/virtualbox-windows11-amd64.box"
}

source "virtualbox-iso" "win11" {
  vm_name          = "win11-packer-${formatdate("YYYY-MM-DD-hh-mm", timestamp())}"
  output_directory = var.temp_disk_path != "" ? var.temp_disk_path : "${var.cache_dir}/windows11/virtualbox-output-${formatdate("YYYY-MM-DD-hh-mm", timestamp())}"
  iso_url          = var.iso_url
  iso_checksum     = "none"

  guest_os_type = "Windows11_64"
  memory        = var.memory
  cpus          = var.cpus
  disk_size     = 61440
  nested_virt   = var.nested_virt

  # SATA is detected reliably by WinPE without driver injection.
  # VirtualBox NVMe (pcie) requires stornvme.sys to be pre-loaded in WinPE,
  # which the stock Windows 11 ISO does not do reliably during setup.
  hard_drive_interface = "sata"
  iso_interface        = "sata"
  firmware             = "efi"

  # Skip attaching/mounting Guest Additions ISO - not needed for a build
  guest_additions_mode = "disable"

  # Performance tuning via VBoxManage:
  # - paravirtprovider hyperv: exposes Hyper-V enlightenments to the Windows guest,
  #   improving timer/scheduler performance even on a non-Hyper-V host
  # - ioapic: required for multi-CPU and improves IRQ handling
  # - disable audio/USB/VRDE: eliminates polling overhead during the build
  vboxmanage = [
    ["modifyvm", "{{.Name}}", "--paravirtprovider", "hyperv"],
    ["modifyvm", "{{.Name}}", "--ioapic", "on"],
    ["modifyvm", "{{.Name}}", "--audio-enabled", "off"],
    ["modifyvm", "{{.Name}}", "--vrde", "off"],
  ]

  communicator   = "winrm"
  winrm_username = "Administrator"
  winrm_password = "P@ssw0rd!"
  winrm_timeout  = "60m"

  # EFI/OVMF takes longer to initialise than BIOS before the
  # "Press any key to boot from CD or DVD" prompt appears.
  boot_wait = "8s"
  boot_command = [
    "<spacebar>"
  ]

  cd_files = [
    "${path.root}/virtualbox/autounattend.xml"
  ]

  shutdown_command = "shutdown /s /t 10 /f /d p:4:1 /c \"Packer Shutdown\""
  shutdown_timeout = "5m"
}

build {
  sources = ["source.virtualbox-iso.win11"]

  post-processor "vagrant" {
    output              = local.box_output
    keep_input_artifact = false
    provider_override   = "virtualbox"
    compression_level   = 1
  }
}
