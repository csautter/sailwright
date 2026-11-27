# Linux lifecycle host test

This synthetic run used Ubuntu 26.04 amd64 with libvirt 12.0.0. Sailwright
built successfully and created, started, and destroyed the request's named
Ubuntu Server VM. The [step records](result.json) identify the commands,
exit codes, durations and binary SHA-256.

The final [cleanup inventory](logs/cleanup.txt) showed no VM created for
this request. The lifecycle scenario could capture that inventory and the
instance name directly to make cleanup easier to verify.
