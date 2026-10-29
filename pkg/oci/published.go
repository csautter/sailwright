package oci

import "fmt"

// PublishedUbuntuRepository holds the Ubuntu artifacts the Linux build workflow
// publishes from main (see ADR 0003). Released CLIs resolve references below it,
// so changing the repository or tag shape breaks them.
const PublishedUbuntuRepository = "ghcr.io/csautter/ubuntu-24"

// PublishedUbuntuReference returns the published reference for an Ubuntu type and architecture.
func PublishedUbuntuReference(ubuntuType, arch string) string {
	return fmt.Sprintf("%s:%s-%s-linux-build", PublishedUbuntuRepository, ubuntuType, arch)
}
