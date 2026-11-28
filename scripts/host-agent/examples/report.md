# Linux lifecycle host test

This synthetic run used Ubuntu 26.04 amd64 with libvirt 12.0.0. Sailwright
built successfully and created, started, and destroyed the Ubuntu Server VM
with CLI selector `sw-linux-lifecycle-a1b2` and libvirt domain
`ubuntu-server-amd64-sw-linux-lifecycle-a1b2-dev-alchemy`. Every lifecycle
command and inventory used `qemu:///system`. The [step records](result.json)
identify the commands, exit codes, durations and binary SHA-256.

The [pre-create and post-destroy inventories](logs/cleanup.txt) both
succeeded and confirmed the exact domain was absent. The lifecycle scenario
could capture those inventories and the full domain name directly.
