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

variable "headless" {
  type    = bool
  default = false
}

variable "vnc_port" {
  type    = number
  default = 5901
}

variable "iso_url" {
  type = string
}

variable "iso_checksum" {
  type        = string
  default     = "none"
  description = "Checksum for iso_url, using Packer's checksum format."
}

variable "use_hardware_acceleration" {
  type        = bool
  default     = true
  description = "Whether to use hardware acceleration when the host can support it."
}

variable "is_ci" {
  type    = bool
  default = env("CI") == "true"
}

variable "ubuntu_type" {
  type        = string
  default     = "server"
  description = "The type of Ubuntu image to build (server or desktop)."
  validation {
    condition     = var.ubuntu_type == "server" || var.ubuntu_type == "desktop"
    error_message = "The variable ubuntu_type must be either 'server' or 'desktop'."
  }
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

locals {
  iso_url             = var.iso_url
  ubuntu_iso_checksum = var.iso_checksum
  cache_directory     = var.cache_dir
  host_is_linux       = var.host_os == "linux"
  host_is_darwin      = var.host_os == "darwin"
  host_same_arch      = var.host_arch == var.arch

  amd64_can_use_native_acceleration = local.host_is_linux && local.host_same_arch && var.use_hardware_acceleration
  amd64_accel                       = local.amd64_can_use_native_acceleration ? "kvm" : "tcg,thread=multi,tb-size=1024"
  amd64_cpu_model                   = local.amd64_can_use_native_acceleration ? "host" : "Skylake-Client"

  arm64_can_use_linux_acceleration  = local.host_is_linux && local.host_same_arch && var.use_hardware_acceleration
  arm64_can_use_darwin_acceleration = local.host_is_darwin && local.host_same_arch && var.use_hardware_acceleration && !var.is_ci
  arm64_can_use_native_acceleration = local.arm64_can_use_linux_acceleration || local.arm64_can_use_darwin_acceleration
  arm64_native_accel                = local.host_is_darwin ? "hvf" : "kvm"
  arm64_software_accel              = local.host_is_darwin && var.is_ci ? "tcg,thread=multi,tb-size=512" : "tcg,thread=multi,tb-size=1024"
  arm64_fallback_cpu_model          = "max,sve=off,sme=off,pauth-impdef=on"
  arm64_efi_code                    = "${local.cache_directory}/qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd"
  arm64_efi_vars                    = "${local.cache_directory}/qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd"

  arm64_accel     = local.arm64_can_use_native_acceleration ? local.arm64_native_accel : local.arm64_software_accel
  arm64_cpu_model = local.arm64_can_use_native_acceleration ? "host" : local.arm64_fallback_cpu_model
  qemu_display    = local.host_is_darwin ? "cocoa" : "none"

  boot_command = {
    "amd64" = [
      "e<wait2>",
      "<leftShiftOn><down><down><down><end><leftShiftOff><wait2>",
      "<leftShiftOn><left><left><left><leftShiftOff><wait> autoinstall ds=nocloud ",
      "<wait2>",
      "<f10><wait>",
    ]
    "arm64" = [
      "e<wait2>",
      "<down><down><down><end><wait2>",
      "${local.left_list}<wait> autoinstall ds=nocloud ",
      "<wait2>",
      "<f10><wait>",
    ]
  }

  qemu_args = {
    "amd64" = [
      ["-machine", "q35,vmport=off,i8042=off,hpet=off"],
      ["-accel", local.amd64_accel],
      ["-smp", "cpus=${var.cpus},cores=${var.cpus},sockets=1,threads=1"],
      ["-global", "PIIX4_PM.disable_s3=1"],
      ["-global", "ICH-LPC.disable_s3=1"],
      ["-device", "qemu-xhci"],
      ["-device", "usb-kbd"],
      ["-device", "usb-tablet"],
      ["-device", "usb-mouse"],
    ]
    "arm64" = [
      ["-accel", local.arm64_accel],
      ["-machine", "virt,highmem=on"],
      ["-cpu", local.arm64_cpu_model],
      ["-drive", "file=${local.arm64_efi_code},if=pflash,unit=0,format=raw,readonly=on"],
      ["-drive", "file={{ .OutputDir }}/efivars.fd,if=pflash,unit=1,format=raw"],
      ["-device", "ramfb"],
      ["-smp", "cpus=${var.cpus},cores=${var.cpus},sockets=1,threads=1"],
      ["-device", "qemu-xhci"],
      ["-device", "usb-kbd"],
      ["-device", "usb-tablet"],
      ["-device", "usb-mouse"],
      ["-device", "virtio-blk-pci,drive=cdrom,bootindex=1"],
      ["-drive", "if=none,id=cdrom,media=cdrom,file=${local.iso_url},readonly=true"],
      ["-device", "virtio-blk-pci,drive=disk,serial=deadbeef,bootindex=0"],
      ["-drive", "if=none,media=disk,id=disk,format=qcow2,file.filename=${local.ubuntu_qcow2},discard=unmap,detect-zeroes=unmap"],
      ["-drive", "if=none,id=cidata,format=raw,file=${path.root}/cloud-init/qemu-${var.ubuntu_type}/cidata.iso,readonly=true"],
      ["-device", "virtio-blk-pci,drive=cidata"],
    ]
  }

  left_list        = join("", [for i in range(0, 16) : "<left>"])
  output_directory = var.build_output_dir != "" ? var.build_output_dir : "${local.cache_directory}/ubuntu/qemu-out-ubuntu-${var.ubuntu_type}-${var.arch}"
  ubuntu_qcow2     = var.artifact_output_path != "" ? var.artifact_output_path : "${local.cache_directory}/ubuntu/qemu-ubuntu-${var.ubuntu_type}-packer-${var.arch}.qcow2"

  # Packages are installed via Packer provisioners (instead of cloud-init) to
  # improve reliability under cross-architecture TCG emulation where the
  # autoinstall phase is extremely slow and has no retry mechanism.
  base_packages = compact(concat(
    ["openssh-server", "linux-virtual", "linux-tools-virtual", "linux-cloud-tools-common", "net-tools", "qemu-guest-agent", "spice-vdagent"],
    var.ubuntu_type == "server" ? ["linux-tools-generic"] : []
  ))
  desktop_packages = compact(concat(
    ["ubuntu-desktop-minimal", "gdm3", "network-manager"],
    var.arch == "amd64" ? ["xserver-xorg-video-qxl"] : []
  ))
}

source "qemu" "ubuntu" {
  qemu_binary       = var.arch == "amd64" ? "qemu-system-x86_64" : "qemu-system-aarch64"
  vm_name           = "linux-ubuntu-${var.ubuntu_type}-packer-${var.arch}"
  headless          = var.headless
  output_directory  = local.output_directory
  iso_url           = local.iso_url
  iso_checksum      = local.ubuntu_iso_checksum
  memory            = var.memory
  cpu_model         = var.arch == "amd64" ? local.amd64_cpu_model : local.arm64_cpu_model
  disk_size         = "64G"
  disk_interface    = "ide"
  format            = "qcow2"
  display           = local.qemu_display
  net_device        = var.arch == "amd64" ? "e1000" : "virtio-net-pci"
  efi_boot          = var.arch == "arm64"
  efi_firmware_code = var.arch == "arm64" ? local.arm64_efi_code : ""
  efi_firmware_vars = var.arch == "arm64" ? local.arm64_efi_vars : ""
  efi_drop_efivars  = var.arch == "arm64"

  cd_label = "cidata"
  cd_files = [
    "${path.root}/cloud-init/qemu-${var.ubuntu_type}/meta-data",
    "${path.root}/cloud-init/qemu-${var.ubuntu_type}/user-data"
  ]

  vnc_bind_address = "127.0.0.1"
  vnc_port_min     = var.vnc_port
  vnc_port_max     = var.vnc_port
  vnc_use_password = true
  vnc_password     = "packer"

  communicator = "ssh"
  ssh_username = "packer"
  ssh_password = "P@ssw0rd!"
  ssh_timeout  = "4h"

  boot_wait        = var.arch == "amd64" ? "2s" : "10s"
  boot_command     = local.boot_command[var.arch]
  shutdown_command = "echo 'P@ssw0rd!' | sudo -S shutdown -P now"
  qemuargs         = local.qemu_args[var.arch]
}

build {
  sources = ["source.qemu.ubuntu"]

  provisioner "shell" {
    environment_vars = ["SUDO_ASKPASS=/tmp/askpass.sh"]
    inline = [
      "printf '#!/bin/sh\necho '\"'\"'P@ssw0rd!'\"'\"'\n' > /tmp/askpass.sh && chmod +x /tmp/askpass.sh",
      "echo 'Waiting for cloud-init to finish...'",
      "sudo -A cloud-init status --wait || true",
    ]
    pause_before = "10s"
    timeout      = "30m"
  }

  provisioner "shell" {
    environment_vars = ["DEBIAN_FRONTEND=noninteractive", "SUDO_ASKPASS=/tmp/askpass.sh"]
    inline = [
      "echo 'Updating package lists...'",
      "sudo -A apt-get update -q",
      "echo 'Installing base packages...'",
      "sudo -A apt-get install -y ${join(" ", local.base_packages)}",
    ]
    max_retries  = 3
    pause_before = "10s"
    timeout      = "60m"
  }

  provisioner "shell" {
    environment_vars = ["DEBIAN_FRONTEND=noninteractive", "SUDO_ASKPASS=/tmp/askpass.sh"]
    inline = var.ubuntu_type == "desktop" ? [
      "echo 'Installing desktop environment and graphics integration packages without recommended packages...'",
      "sudo -A apt-get install -y --no-install-recommends ${join(" ", local.desktop_packages)}",
    ] : ["echo 'Server build - skipping desktop packages.'"]
    max_retries  = 2
    pause_before = "10s"
    timeout      = "120m"
  }

  provisioner "shell" {
    environment_vars = ["SUDO_ASKPASS=/tmp/askpass.sh"]
    inline = var.ubuntu_type == "desktop" ? [
      "echo 'Configuring desktop netplan to use NetworkManager on future boots...'",
      "printf 'network:\\n  version: 2\\n  renderer: NetworkManager\\n' > /tmp/90-dev-alchemy-networkmanager.yaml",
      "sudo -A install -m 600 /tmp/90-dev-alchemy-networkmanager.yaml /etc/netplan/90-dev-alchemy-networkmanager.yaml",
      "sudo -A systemctl enable NetworkManager.service",
    ] : ["echo 'Server build - keeping default netplan renderer.'"]
    pause_before = "5s"
    timeout      = "10m"
  }

  post-processor "shell-local" {
    inline = var.arch == "amd64" ? [
      "echo 'Exporting QCOW2 image...'",
      "mkdir -p \"$(dirname \"${local.ubuntu_qcow2}\")\"",
      "cp \"${local.output_directory}\"/linux-ubuntu-${var.ubuntu_type}-packer-* \"${local.ubuntu_qcow2}\"",
      "echo 'Export completed.'"
      ] : [
      "echo 'No export needed for arm64 architecture.'"
    ]
  }
}
