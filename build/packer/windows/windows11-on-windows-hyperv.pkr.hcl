packer {
  required_version = ">= 1.14.0"
  required_plugins {
    hyperv = {
      version = ">= 1.1.5"
      source  = "github.com/hashicorp/hyperv"
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
  temp_dir   = var.temp_disk_path != "" ? var.temp_disk_path : "${var.cache_dir}/windows11/hyperv-temp"
  box_output = var.artifact_output_path != "" ? var.artifact_output_path : "${var.cache_dir}/windows11/hyperv-windows11-amd64.box"
}

source "hyperv-iso" "win11" {
  vm_name          = "win11-packer-${formatdate("YYYY-MM-DD-hh-mm", timestamp())}"
  output_directory = "${var.cache_dir}/windows11/hyperv-output-${formatdate("YYYY-MM-DD-hh-mm", timestamp())}"
  temp_path        = local.temp_dir

  iso_url      = var.iso_url
  iso_checksum = "none"
  # Ensure that the "Default Switch" exists in Hyper-V.
  # You can check in Hyper-V Manager under "Virtual Switch Manager".
  # If it does not exist, create a new virtual switch named "Default Switch".
  switch_name = "Default Switch"
  memory      = var.memory
  cpus        = var.cpus
  disk_size   = 61440

  communicator   = "winrm"
  winrm_username = "Administrator"
  winrm_password = "P@ssw0rd!"
  winrm_timeout  = "60m"

  enable_secure_boot = true
  generation         = 2
  enable_tpm         = true

  boot_wait = "500ms"

  # Send multiple keypresses to ensure we catch the "press any key" prompt
  # The prompt typically has a 5-second timeout window
  boot_command = [
    "<spacebar><wait1s>",
    "<spacebar><wait1s>",
    "<spacebar><wait1s>",
    "<spacebar>"
  ]

  # The "autounattend.xml" file is an unattended setup configuration for Windows installation.
  cd_files = [
    "${path.root}/hyperv/autounattend.xml"
  ]

  shutdown_command = "shutdown /s /t 10 /f /d p:4:1 /c \"Packer Shutdown\""
  shutdown_timeout = "5m"
}

build {
  sources = ["source.hyperv-iso.win11"]

  post-processor "vagrant" {
    output              = local.box_output
    keep_input_artifact = false
    provider_override   = "hyperv"
    compression_level   = 1
  }
}
