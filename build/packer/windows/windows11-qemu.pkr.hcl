packer {
  required_plugins {
    qemu = {
      version = ">= 1.1.6"
      source  = "github.com/hashicorp/qemu"
    }
  }
}

variable "host_arch" {
  type        = string
  description = "Normalized host architecture: amd64 or arm64."
  validation {
    condition     = var.host_arch == "amd64" || var.host_arch == "arm64"
    error_message = "The variable host_arch must be either 'amd64' or 'arm64'."
  }
}

variable "host_os" {
  type        = string
  description = "Normalized host OS: linux or darwin."
  validation {
    condition     = var.host_os == "linux" || var.host_os == "darwin"
    error_message = "The variable host_os must be either 'linux' or 'darwin'."
  }
}

variable "arch" {
  type        = string
  default     = "amd64"
  description = "Target architecture: amd64 or arm64."
  validation {
    condition     = var.arch == "amd64" || var.arch == "arm64"
    error_message = "The variable arch must be either 'amd64' or 'arm64'."
  }
}

variable "iso_url" {
  type        = string
  default     = ""
  description = "Path to Windows 11 ISO. If empty, will be set by arch."
}

variable "use_hardware_acceleration" {
  type        = bool
  default     = true
  description = "Whether to use hardware acceleration when the host can support it."
}

variable "headless" {
  type    = bool
  default = false
}

variable "vnc_port" {
  type    = number
  default = 5901
}

variable "cpus" {
  type    = number
  default = 4
}

variable "memory" {
  type        = number
  default     = 4096
  description = "Memory in MB to allocate to the VM"
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

variable "build_output_dir" {
  type        = string
  default     = ""
  description = "Optional short-lived Packer output directory."
}

variable "artifact_output_path" {
  type        = string
  default     = ""
  description = "Optional QCOW2 artifact path. Used to stage no-cache rebuilds before promotion."
}

variable "is_ci" {
  type    = bool
  default = env("CI") == "true"
}

locals {
  cache_directory = var.cache_dir
  host_is_linux   = var.host_os == "linux"
  host_is_darwin  = var.host_os == "darwin"
  host_same_arch  = var.host_arch == var.arch

  win11_default_iso = {
    amd64 = "${local.cache_directory}/windows11/iso/win11_25h2_english_amd64.iso"
    arm64 = "${local.cache_directory}/windows11/iso/Win11_ARM64_Unattended.iso"
  }
  win11_iso         = var.iso_url != "" ? var.iso_url : local.win11_default_iso[var.arch]
  win11_qcow2       = var.artifact_output_path != "" ? var.artifact_output_path : "${local.cache_directory}/windows11/qemu-windows11-${var.arch}.qcow2"
  win11_guest_tools = "${local.cache_directory}/utm/utm-guest-tools-latest.iso"
  win11_virtio_iso  = "${local.cache_directory}/windows/virtio-win.iso"
  win11_uefi_code   = "${local.cache_directory}/qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd"
  win11_uefi_vars   = "${local.cache_directory}/qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd"

  amd64_can_use_native_acceleration = local.host_is_linux && local.host_same_arch && var.use_hardware_acceleration
  amd64_accelerator                 = local.amd64_can_use_native_acceleration ? "kvm" : "tcg"
  amd64_cpu_model                   = local.amd64_can_use_native_acceleration ? "host" : "Haswell"

  arm64_can_use_linux_acceleration  = local.host_is_linux && local.host_same_arch && var.use_hardware_acceleration
  arm64_can_use_darwin_acceleration = local.host_is_darwin && local.host_same_arch && var.use_hardware_acceleration && !var.is_ci
  arm64_can_use_native_acceleration = local.arm64_can_use_linux_acceleration || local.arm64_can_use_darwin_acceleration
  arm64_native_accelerator          = local.host_is_darwin ? "hvf" : "kvm"
  arm64_accelerator                 = local.arm64_can_use_native_acceleration ? local.arm64_native_accelerator : "tcg,thread=multi,tb-size=512"
  arm64_cpu_model                   = local.arm64_can_use_native_acceleration ? "host" : "max,sve=off,sme=off,pauth-impdef=on"
  arm64_install_bootindex           = local.arm64_can_use_native_acceleration ? 1 : 0
  arm64_disk_bootindex              = local.arm64_can_use_native_acceleration ? 0 : 1

  qemu_display = local.host_is_darwin && !var.headless ? "cocoa" : "none"

  qemu_args = {
    "amd64" = [
      ["-device", "qemu-xhci,id=usb"],
      ["-device", "usb-storage,drive=install,removable=true,bootindex=0"],
      ["-drive", "if=none,id=install,format=raw,media=cdrom,file=${local.win11_iso},readonly=true"],
      ["-device", "usb-storage,drive=virtio-drivers,removable=true,bootindex=2"],
      ["-drive", "if=none,id=virtio-drivers,format=raw,media=cdrom,file=${local.win11_virtio_iso},readonly=true"],
      ["-device", "usb-storage,drive=utm-tools,removable=true,bootindex=3"],
      ["-drive", "if=none,id=utm-tools,format=raw,media=cdrom,file=${local.win11_guest_tools},readonly=true"],
      ["-device", "ide-hd,drive=ide0,bootindex=1"],
      ["-drive", "if=none,media=disk,id=ide0,format=qcow2,file.filename=${local.win11_qcow2},discard=unmap,detect-zeroes=unmap"],
      ["-boot", "order=c,menu=on"],
      ["-k", "de"]
    ],
    "arm64" = [
      ["-accel", local.arm64_accelerator],
      ["-machine", "virt,highmem=on"],
      ["-cpu", local.arm64_cpu_model],
      ["-drive", "file=${local.win11_uefi_code},if=pflash,unit=0,format=raw,readonly=on"],
      ["-drive", "file={{ .OutputDir }}/efivars.fd,if=pflash,unit=1,format=raw"],
      ["-device", "ramfb"],
      ["-device", "qemu-xhci"],
      ["-device", "usb-kbd"],
      ["-device", "usb-tablet"],
      ["-device", "usb-storage,drive=install,removable=true,bootindex=${local.arm64_install_bootindex}"],
      ["-drive", "if=none,id=install,format=raw,media=cdrom,file=${local.win11_iso},readonly=true"],
      ["-device", "usb-storage,drive=virtio-drivers,removable=true"],
      ["-drive", "if=none,id=virtio-drivers,format=raw,media=cdrom,file=${local.win11_virtio_iso},readonly=true"],
      ["-device", "usb-storage,drive=utm-tools,removable=true"],
      ["-drive", "if=none,id=utm-tools,format=raw,media=cdrom,file=${local.win11_guest_tools},readonly=true"],
      ["-device", "nvme,drive=nvme0,serial=deadbeef,bootindex=${local.arm64_disk_bootindex}"],
      ["-drive", "if=none,media=disk,id=nvme0,format=qcow2,file.filename=${local.win11_qcow2},discard=unmap,detect-zeroes=unmap"],
      ["-k", "de"]
    ]
  }
}

source "qemu" "win11" {
  vm_name = "windows11-packer-${var.arch}"

  headless         = var.headless
  iso_url          = local.win11_iso
  iso_checksum     = "none"
  output_directory = var.build_output_dir != "" ? var.build_output_dir : "${local.cache_directory}/windows11/qemu-out-windows11-${var.arch}"
  display          = local.qemu_display
  memory           = var.memory
  cores            = var.cpus
  vnc_bind_address = "127.0.0.1"
  vnc_port_min     = var.vnc_port
  vnc_port_max     = var.vnc_port
  vnc_use_password = true
  vnc_password     = "packer"
  boot_wait        = "5s"
  boot_command = var.arch == "arm64" ? [
    "<spacebar>",
    "<wait3>",
    "<spacebar>",
    "<wait3>",
    "<spacebar>",
  ] : null
  communicator            = "winrm"
  pause_before_connecting = var.is_ci ? "10m" : "1m"
  winrm_username          = "Administrator"
  winrm_password          = "P@ssw0rd!"
  winrm_timeout           = "4h"
  shutdown_command        = "shutdown /s /t 60 /f /d p:4:1 /c \"Packer Shutdown\""
  shutdown_timeout        = "10m"

  machine_type   = var.arch == "amd64" ? "q35" : null
  accelerator    = var.arch == "amd64" ? local.amd64_accelerator : null
  cpu_model      = var.arch == "amd64" ? local.amd64_cpu_model : null
  qemu_binary    = var.arch == "arm64" ? "qemu-system-aarch64" : "qemu-system-x86_64"
  disk_size      = "64G"
  disk_interface = var.arch == "amd64" ? "ide" : null
  format         = "qcow2"
  net_device     = var.arch == "amd64" ? "e1000" : "virtio-net-pci"

  efi_boot          = var.arch == "arm64"
  efi_firmware_code = var.arch == "arm64" ? local.win11_uefi_code : ""
  efi_firmware_vars = var.arch == "arm64" ? local.win11_uefi_vars : ""
  efi_drop_efivars  = var.arch == "arm64"

  tpm_device_type = var.arch == "amd64" ? "emulator" : null

  floppy_files = var.arch == "amd64" ? ["${path.root}/qemu-amd64/autounattend.xml"] : null

  qemuargs = local.qemu_args[var.arch]
}

build {
  sources = ["source.qemu.win11"]
}
