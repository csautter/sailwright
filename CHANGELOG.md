# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.27.0](https://github.com/csautter/sailwright/compare/v0.26.0...v0.27.0) (2026-11-12)


### ⚠ BREAKING CHANGES

* **runner:** delete_resource_group returns JSON errors with 400/403 (and mapped dependency statuses) instead of plain-text 500. request_runner returns 409 for an existing resource group without the broker ownership tag instead of adopting it.

### Added

* **cli:** add verified self-updates and opt-in release checks ([ea8be51](https://github.com/csautter/sailwright/commit/ea8be5194b9285f67512fbb9b6db026f9cde25df))
* **cli:** add verified self-updates and opt-in release checks ([cbc1e68](https://github.com/csautter/sailwright/commit/cbc1e68f382419e2046c21f3167542810a5163a0))
* **cli:** coordinate VM and artifact operations across processes ([bfe5c0a](https://github.com/csautter/sailwright/commit/bfe5c0a44e170382d5aa25e68d07e81a01f1e611))
* **cli:** expose running VM connection details and native Hyper-V discovery ([2779abb](https://github.com/csautter/sailwright/commit/2779abbdd3929d272c970150a064e8d2070b3515))
* **cli:** mask the guest password in vm info text output ([59ab52a](https://github.com/csautter/sailwright/commit/59ab52a7bce404e8164a422d8431201df8cdf6a9))
* **cli:** report and guard unsafe guest network access ([b1c6120](https://github.com/csautter/sailwright/commit/b1c61204ccc803861a70519bd86673d8e01fd8f3))
* **cli:** select and list named VM instances with isolated locks ([a717078](https://github.com/csautter/sailwright/commit/a71707855be7be46252e3d83a0c7fdc447449d5a))
* **cli:** wait for busy operation locks and name the lock holder ([272516a](https://github.com/csautter/sailwright/commit/272516aa89990a0f2220c66c32b53300cd6c4a1b))
* **cmd:** report whether the VM exists in create list --json ([7713c70](https://github.com/csautter/sailwright/commit/7713c70c653f34b671c2bf240861f94373ac9e25))
* **deploy:** enable Hyper-V enlightenments for Windows libvirt guests ([c476d91](https://github.com/csautter/sailwright/commit/c476d91b04568744df3b6024742e75a51fb73abb))
* **devcontainer:** install the Claude Code CLI in the image ([a80f3e2](https://github.com/csautter/sailwright/commit/a80f3e2a65630a720f2adb0433939c83c81ac442))
* **devcontainer:** run Orca workspaces in devcontainer-based Docker containers ([fbecde3](https://github.com/csautter/sailwright/commit/fbecde3521850d8c102d11d41cd82b3cf6b2bf9d))
* **devcontainer:** share a GitHub token with devcontainers and Orca workspaces ([2b1f77f](https://github.com/csautter/sailwright/commit/2b1f77fd2f312915c9e5dc64bc4d870c22cf02a2))
* **gateway:** add managed guest SSH and WinRM tunnels ([727e994](https://github.com/csautter/sailwright/commit/727e994015c7e9d2dcbf08a055d6163ba1177b70))
* **gateway:** add managed VM access with ownership checks ([8c4cfd5](https://github.com/csautter/sailwright/commit/8c4cfd5ccb8788c3939abeff320efb4831b1d636))
* **gateway:** add serve --bypass-approvals for disposable test hosts ([bca36c6](https://github.com/csautter/sailwright/commit/bca36c6404004625b9747e9f768193c4412c9923))
* **gateway:** address named instances and enforce a create budget ([82f701f](https://github.com/csautter/sailwright/commit/82f701fea9129eb75b21225f154885e0ae26c4ce))
* **gateway:** discover same-machine gateways alongside devcontainers ([998ee55](https://github.com/csautter/sailwright/commit/998ee55a164552df9b51b4eb547c9f99bed79411))
* **gateway:** require operator approval for VMs the gateway did not create ([87ddf1c](https://github.com/csautter/sailwright/commit/87ddf1cd68f4073439c7be4c455df2de39a7ed5d))
* **gateway:** share CLI data with compatibility checks and private config ([0d21b5b](https://github.com/csautter/sailwright/commit/0d21b5b137111a6f4f9dcac20afa20b93de1af5b))
* **gateway:** share SSH connections in the generated ssh_config ([f5ca9fc](https://github.com/csautter/sailwright/commit/f5ca9fcbb0d204ead0497024be4da4a7712d9281))
* **install:** install oscdimg with the Hyper-V setup on Windows ([3d3c65b](https://github.com/csautter/sailwright/commit/3d3c65ba8fe190d9acd9ff0f9e736a925635f6b6))
* **install:** offer opt-in Hyper-V setup on Windows ([ca1969d](https://github.com/csautter/sailwright/commit/ca1969d28d0356b0fdabb608c9cf95a07b07c4e5))
* **install:** offer opt-in Vagrant setup on Windows ([625686a](https://github.com/csautter/sailwright/commit/625686a17fceb22716f6ca3849f09fbf11904ef9))
* **macos:** add Linux profile validation and tiered native CI ([a3c8082](https://github.com/csautter/sailwright/commit/a3c8082558625877bb66f5d4cd684a8489fd7d44))
* orca workspaces ([8651774](https://github.com/csautter/sailwright/commit/865177472b137d049f1ae2e2451e47e3d682c294))
* **runner:** expose provisioning status after VM acceptance ([0d31347](https://github.com/csautter/sailwright/commit/0d31347cac10f8931f1056847534c29a8890c729))
* **security:** guard exposed guests with non-private credentials ([2ac816e](https://github.com/csautter/sailwright/commit/2ac816ea818bc8a10a7b963ffced7463211ba4fe))
* **security:** inspect guest credentials and vm network exposure ([29ed061](https://github.com/csautter/sailwright/commit/29ed061eb7c8d2be8db17b24dc90a8e68f8790a5))
* **selfupdate:** report signature verification level ([edca708](https://github.com/csautter/sailwright/commit/edca708e02d81d25f812f881907241956ff7e091))
* **utm:** allow an explicit bundle destination ([0635987](https://github.com/csautter/sailwright/commit/0635987b03a561bd1a2b7351cb4adf6375a9e9b0))
* **vm:** derive independent named instances across providers ([1c865c8](https://github.com/csautter/sailwright/commit/1c865c8e75a9f5482147de734ebc7e2be7f779b1))
* **vm:** initialize guest hostnames and guard shared provider resources ([6f6c8fb](https://github.com/csautter/sailwright/commit/6f6c8fbbe032eaa7704ca0c9245029831ac7b4bc))
* **vm:** support independent named VM instances ([b9049b4](https://github.com/csautter/sailwright/commit/b9049b47fcc7208ae1d0cc8f49180cba233a0344))


### Fixed

* **brew:** install Homebrew on Intel macOS without taking over /usr/local ([9515811](https://github.com/csautter/sailwright/commit/9515811e399ea03da38eccc42eaee09e38370c10))
* **build:** bind qemu guest port forwards to loopback ([3587289](https://github.com/csautter/sailwright/commit/3587289733d19475d9b8e46c9817a96e2ceef88c))
* **build:** cache virtio-win ISO under a versioned file name ([df6fd77](https://github.com/csautter/sailwright/commit/df6fd77d81104cb0047e0f30136d5573efc65101))
* **build:** cache virtio-win ISO under a versioned file name ([1465ead](https://github.com/csautter/sailwright/commit/1465ead81479fc56109023fcd305f46f5b1f198b))
* **build:** declare the vagrant plugin for the Ubuntu Hyper-V template ([a0b11b9](https://github.com/csautter/sailwright/commit/a0b11b910ba310acf118d22bbef1fe527eeb29b8))
* **build:** download dependencies shared by parallel builds only once ([b525e85](https://github.com/csautter/sailwright/commit/b525e85de1d7d40efc7f45988bca0ed105eb6367))
* **build:** drop linux-azure from the Ubuntu Hyper-V images ([e9ec953](https://github.com/csautter/sailwright/commit/e9ec953f5baee0c881a6e30c5a28d2fa9f100055))
* **build:** get Windows 11 Hyper-V builds through OOBE on slow hosts ([575d899](https://github.com/csautter/sailwright/commit/575d8993f9b714aca4c052cd258e56f2966734cb))
* **build:** honor headless mode across all builds ([69cec36](https://github.com/csautter/sailwright/commit/69cec369a9a1d7617a835325c004e46defff194b))
* **build:** isolate parallel download clients and repair release checks ([caf54eb](https://github.com/csautter/sailwright/commit/caf54eb33cbafa98988d674b01479cf6a98f7ad2))
* **build:** keep a terminal build that finished as Ctrl+C arrived ([b2cfd8a](https://github.com/csautter/sailwright/commit/b2cfd8ab048b5a0952eb62745ea154bd95a8d6f8))
* **build:** keep artifact state when a promotion changes nothing ([b77ea4e](https://github.com/csautter/sailwright/commit/b77ea4e99db97e55f363c6ff5cc53980c4ac8ba3))
* **build:** keep builds that finished before an interrupt, longer stop grace for Ctrl+C ([b9c2016](https://github.com/csautter/sailwright/commit/b9c20169ccd80fea1603f5b803dd324ff2d28a6b))
* **build:** keep headless builds free of display windows ([c622464](https://github.com/csautter/sailwright/commit/c62246463d58a926758fb8ca49ad8684225f8725))
* **build:** keep pressing a key until the Windows 11 Hyper-V DVD boots ([d342fde](https://github.com/csautter/sailwright/commit/d342fde9c7d981f016247ab19da4a4480d15e132))
* **build:** kill cancelled Windows builds, reuse interrupt handler in post-processing ([0804dce](https://github.com/csautter/sailwright/commit/0804dceb118ce98d0fa18c48ade4fa9360dfa019))
* **build:** leave SIGQUIT (Ctrl+\) to Go's default goroutine dump ([c2ccdc6](https://github.com/csautter/sailwright/commit/c2ccdc62941a43759a6355fc9d8655f4e77bd7e9))
* **build:** mark artifact sets pending before promoting builds and pulls ([41da555](https://github.com/csautter/sailwright/commit/41da5555a102aae797c949d69901992c153e8248))
* **build:** match cache file names without globbing the cache directory ([4bc80b4](https://github.com/csautter/sailwright/commit/4bc80b4b54937ba9e0d13fdcac8b3ad5fab704d7))
* **build:** propagate headless mode to all builds ([51d7ac0](https://github.com/csautter/sailwright/commit/51d7ac0fe0dc2840ae8d4805fcdc2a6c6c882edc))
* **build:** prune outdated virtio-win ISOs from the cache ([4443081](https://github.com/csautter/sailwright/commit/4443081e3ac20671c723731931e310d5c553a2f7))
* **build:** skip artifact promotion for packer startup probes ([7b3ecbb](https://github.com/csautter/sailwright/commit/7b3ecbbc71bebe758a8b72af4ef96e30d376c4c2))
* **build:** stop builds cleanly on interrupt and never leave complete-looking artifacts ([0b1aca4](https://github.com/csautter/sailwright/commit/0b1aca429ceba9c3707f25245853e787e2693cb8))
* **build:** stop builds cleanly on interrupt and never leave complete-looking artifacts ([d36a0a9](https://github.com/csautter/sailwright/commit/d36a0a9ccb39cbcc2a7254a54e3b214ffa633e65))
* **build:** version firmware and guest tools caches ([66cfb86](https://github.com/csautter/sailwright/commit/66cfb86dc864bf0b4284a3805c927ded28360ebd))
* **build:** wait up to 4h for WinRM in the Windows 11 Hyper-V build ([8aaa5c6](https://github.com/csautter/sailwright/commit/8aaa5c6a5a5643f5e7fa0a589734606e7bd88e89))
* **build:** warn when obsolete cache files cannot be removed ([84ed970](https://github.com/csautter/sailwright/commit/84ed970a029fc535b515b6a5e579156fa4962548))
* **ci:** align macOS profile checks with main ([7f981e9](https://github.com/csautter/sailwright/commit/7f981e99e15d493521ab8c2d1394c2a6767a44a2))
* **ci:** keep unrelated labels from cancelling macOS jobs ([e82af82](https://github.com/csautter/sailwright/commit/e82af823d05f8e12d03062588566aadeb7052aa6))
* **ci:** reclaim runner disk space and reorder cache downloads in linux build workflow ([8da068c](https://github.com/csautter/sailwright/commit/8da068c7039ccbada63baa4eaeedc2c6f9f73869))
* **ci:** retry transient tool download failures ([54f65d4](https://github.com/csautter/sailwright/commit/54f65d46f6076bcb40ea3f8f520ced488b1c0c86))
* **ci:** route shared Hyper-V build code and tighten preflight permissions ([158da09](https://github.com/csautter/sailwright/commit/158da0930d079ca2d7c0d3d42b4ecfcaba0230e6))
* **cli:** choose the host-reachable Hyper-V address and report network modes ([a1e03f2](https://github.com/csautter/sailwright/commit/a1e03f270af77227d5c1f5d95c7560e8778083df))
* **cli:** clarify update verification and split the update command ([109808b](https://github.com/csautter/sailwright/commit/109808b10cc8d70c0a0f7f8460538ff63c0c4d12))
* **cli:** derive network modes from active VM configuration ([ad0500e](https://github.com/csautter/sailwright/commit/ad0500e02f5d76eb3199621e04034485ee0af300))
* **cli:** mark local provisioning on macOS and Linux as stable ([875eed3](https://github.com/csautter/sailwright/commit/875eed38a8d7ba700245ba78349cea0d0a986dcc))
* **cli:** name the invalid value when a guest port cannot be parsed ([d8f3caf](https://github.com/csautter/sailwright/commit/d8f3caf20293ff5dcd859b5d4a973e576a9c0b99))
* **cli:** report guest security accurately in VM listings ([80a7de7](https://github.com/csautter/sailwright/commit/80a7de75dfe6de0948537f3b02f2a4bd26aeceda))
* complete fresh-host provisioning checks ([02c85c6](https://github.com/csautter/sailwright/commit/02c85c6368c6c24cbe48aa4d3778adfa268b1c36))
* **deploy:** account for exposed UTM port forwards ([94d6ba4](https://github.com/csautter/sailwright/commit/94d6ba43b41f0433379336bc87619adebb4a2d4d))
* **deploy:** classify ambiguous Tart networking conservatively ([abf1b6f](https://github.com/csautter/sailwright/commit/abf1b6feb3e5527d3c86ccb3f190015b84114e5c))
* **deploy:** detect routed IPv6 in libvirt NAT networks ([275d299](https://github.com/csautter/sailwright/commit/275d2995430706ea56710f3c11382ac23be1c443))
* **deploy:** list Tart destroy targets as absent without tart installed ([e563cb0](https://github.com/csautter/sailwright/commit/e563cb0cf06fb691402ff09b41606744467a6d3b))
* **deploy:** list Tart destroy targets as absent without tart installed ([f3bb90b](https://github.com/csautter/sailwright/commit/f3bb90bb60057a48d51ac9e4d6b401c7b05af6dc))
* **deploy:** remember the Hyper-V nested virtualization fallback per VM ([7334b34](https://github.com/csautter/sailwright/commit/7334b3478c4812316181a71d3ad060e812963703))
* **deploy:** resolve UTM bookmarks and harden release checks ([f814187](https://github.com/csautter/sailwright/commit/f8141872da094767c8ac6e04d32138dc12fbdfcf))
* **deploy:** retry Hyper-V starts without nested virtualization when the host refuses it ([a56089f](https://github.com/csautter/sailwright/commit/a56089f5fba3c53965ce7c3d569e675bfbe505e4))
* **deploy:** wait for UTM preferences to drop a deleted registration ([2e2d438](https://github.com/csautter/sailwright/commit/2e2d43877dc1579c3dfbdd9da11e8e804aab4b36))
* **deps:** remediate compress panic and x/net security alerts ([1df6758](https://github.com/csautter/sailwright/commit/1df675863ff02aec0ce7921acc37be2a892143df))
* **deps:** update compress and x/net for security advisories ([9f2dbb5](https://github.com/csautter/sailwright/commit/9f2dbb5bacc434c248481861be3918c56848d8af))
* **devcontainer:** mount ~/.agents/agents and skip extension install in Orca ([ce3aed2](https://github.com/csautter/sailwright/commit/ce3aed20ac9f6c1598110badd6811e820bf2a3e2))
* **gateway:** allow slow vm info and vm identity runs on Hyper-V hosts ([00a4779](https://github.com/csautter/sailwright/commit/00a4779135e398efd7967848b765bccb11dac263))
* **gateway:** audit and clean up credentials before releasing jobs ([b1efe58](https://github.com/csautter/sailwright/commit/b1efe5845b3147857b74c36f6e6766c1d7f8f97b))
* **gateway:** bind VM access to hypervisor instance identity ([eef9b73](https://github.com/csautter/sailwright/commit/eef9b7338cf6594324bb9856b8e9a96db5e2f8b1))
* **gateway:** cancel and await VM lookups on shutdown ([8ef4e5a](https://github.com/csautter/sailwright/commit/8ef4e5afb79da3e77f66a343a1cd5f17f4446621))
* **gateway:** confirm VM instances inside lifecycle jobs, not the tool call ([c17c9ba](https://github.com/csautter/sailwright/commit/c17c9ba6137b8a87fd91a12d4f95f2c0aa16c33c))
* **gateway:** gate foreign VMs with isolated state too ([c9537be](https://github.com/csautter/sailwright/commit/c9537beb696f6e1749099a6818d4e41b40f8d2bc))
* **gateway:** gate foreign VMs with isolated state too ([ae56d8f](https://github.com/csautter/sailwright/commit/ae56d8f1d79121803b5847dca1f42e3b890709c1))
* **gateway:** isolate embedded assets and honor CLI directory overrides ([3c4850d](https://github.com/csautter/sailwright/commit/3c4850d7b46865e8280100119ca5d68ded47aaa6))
* **gateway:** keep existing isolated state on upgrade and accept older installed CLIs ([620566c](https://github.com/csautter/sailwright/commit/620566c2956f4d6bd6aae616e1038d22eefc5a2d))
* **gateway:** keep fresh VM lookups from joining stale in-flight queries ([c3d2c07](https://github.com/csautter/sailwright/commit/c3d2c072ffa7abf236825cb31ee985222c3a9b6c))
* **gateway:** keep passwords equal to the username out of redaction ([a8adbe4](https://github.com/csautter/sailwright/commit/a8adbe4e1368f8a093ce955cb342ceef692e76d4))
* **gateway:** limit tunnels to host subnets and protocol ports ([eda8dc2](https://github.com/csautter/sailwright/commit/eda8dc2703cd4590b7db2e4f2bc89aa96e07cd54))
* **gateway:** print a PowerShell live-output command on Windows hosts ([b05680b](https://github.com/csautter/sailwright/commit/b05680b8b3e849f679ba0923c862c900346cbb8a))
* **gateway:** print a PowerShell live-output command on Windows hosts ([d149e8a](https://github.com/csautter/sailwright/commit/d149e8a7eb2db8b0d1b3534393367d6d83160490))
* **gateway:** publish job done and free slot in one critical section ([fa14a33](https://github.com/csautter/sailwright/commit/fa14a33f927162b6a133f3ea603142fef7d76e6e))
* **gateway:** release job slot and snapshot before reporting done ([3662853](https://github.com/csautter/sailwright/commit/3662853937b86bcbd14ea9117a36a3a3a9f3acfd))
* **gateway:** release job slot and snapshot before reporting done ([f5520c2](https://github.com/csautter/sailwright/commit/f5520c2b8d2925047a2012144fef1636a2d34782))
* **gateway:** report completed jobs as inactive ([92dfc53](https://github.com/csautter/sailwright/commit/92dfc539a61ec460c899a4388469612813fc817d))
* **gateway:** report why a VM lookup failed ([6f1d350](https://github.com/csautter/sailwright/commit/6f1d35030072bd52698250ed7a60b46180f2fc25))
* **gateway:** show bypassed approvals in the serve banner ([c6a17a5](https://github.com/csautter/sailwright/commit/c6a17a55900cfc7760cc55c80042f5012dd4face))
* **gateway:** show why a tunnel was refused ([bceb88a](https://github.com/csautter/sailwright/commit/bceb88aed4bf1d8bce78c816a9c787978074f2e5))
* **gateway:** stop marking vm_info as read-only ([6c38a7a](https://github.com/csautter/sailwright/commit/6c38a7a5780b10ea2752a8d00e53dcce75715023))
* **gateway:** stop redacting common words such as "admin" ([79da4fd](https://github.com/csautter/sailwright/commit/79da4fd519c0022f5de19c0ddce66ecdf6eed7c9))
* **gateway:** tell agents and operators that isolated gateways gate foreign VMs ([40a885f](https://github.com/csautter/sailwright/commit/40a885f62adfffeba6f6af07304463b6c590ae3b))
* **gateway:** treat update --check as read-only in risk hints ([8149044](https://github.com/csautter/sailwright/commit/8149044a8dfacdb4a095368a12ca7222f293fef6))
* **gateway:** version the changed Windows lock protocol ([a978701](https://github.com/csautter/sailwright/commit/a978701ac7d4c5db72149dc02d96fe8a5b0151e5))
* install isolated Ansible on Linux and validate ansible-core versions ([b73c97b](https://github.com/csautter/sailwright/commit/b73c97b80d50c8fd2d08c745285e857cb3f7c278))
* **install:** skip absent drives when locating Cygwin on Windows ([1e9365f](https://github.com/csautter/sailwright/commit/1e9365facfd01fba57e9fa2e9a0b677ee1faa603))
* **install:** verify the Windows ADK installer signature before running it ([a65f178](https://github.com/csautter/sailwright/commit/a65f178a74e1fab035d63268aaccb05f15a49f00))
* **java:** install macOS Temurin packages through become with verified checksums ([ff1d92c](https://github.com/csautter/sailwright/commit/ff1d92cba03edb5617abd04e7c3f4bdf887ef2bc))
* **java:** register alternatives without repeatedly changing selection ([dceff55](https://github.com/csautter/sailwright/commit/dceff55fc9aff4b7e6f3d95c658a6e9bb73b1661))
* **java:** require an exact Java home match on macOS ([a8f1dc0](https://github.com/csautter/sailwright/commit/a8f1dc01b2c14809e38d52e3ab44c361f01ccb73))
* **macos:** address lifecycle safety and CI review findings ([7c5a0c1](https://github.com/csautter/sailwright/commit/7c5a0c1192ee9661b3e475bf26e50fbf6191a394))
* **macos:** keep UTM creation offline and confine Tart identity lookup ([006e10c](https://github.com/csautter/sailwright/commit/006e10c1e0ca5e62f39d45ac866ee27fd0354bd0))
* **macos:** repair UTM lifecycle and bind gateway ownership to instances ([c702441](https://github.com/csautter/sailwright/commit/c7024419f86f92315ec406606901e5157b06ae5e))
* **macos:** repair UTM lifecycle and bind gateway ownership to instances ([39ce53d](https://github.com/csautter/sailwright/commit/39ce53df0acda0e39e818069e7428a8f9a8024f6))
* **macos:** verify UTM registration paths and detect unloaded entries ([4afe62d](https://github.com/csautter/sailwright/commit/4afe62dda1331efaba783b845087ff8701ffc3e7))
* **oci:** make failed response close explicitly best effort ([578a592](https://github.com/csautter/sailwright/commit/578a592ae92bdb707334abd45ed1c9004ca12877))
* **oci:** make failed response close explicitly best effort ([610b8f0](https://github.com/csautter/sailwright/commit/610b8f01c7d65d4cc254a6bc190f96d23387a7df))
* **oci:** resume interrupted blob downloads ([cd0ce78](https://github.com/csautter/sailwright/commit/cd0ce78404f3b7c9e2271c62bdadbf1d3d35ea06))
* **oci:** use http/1.1 for registry transfers ([8fa0e23](https://github.com/csautter/sailwright/commit/8fa0e2306569e3ddbb86d14c99f930509d2cce79))
* **orca:** allow --dangerously-skip-permissions in root workspace containers ([545a83a](https://github.com/csautter/sailwright/commit/545a83a4da57dac71c2f9bcf91928d92eebc88d6))
* **orca:** resolve ShellCheck findings in orca scripts ([595b23b](https://github.com/csautter/sailwright/commit/595b23b7719a47dd9f00b8b4698ac0c874d4b39a))
* **provision:** exclude braces from generated Windows provision passwords ([fe57289](https://github.com/csautter/sailwright/commit/fe5728993953ebf4de655f9fa986017f0e12dc8f))
* **provision:** exclude braces from generated Windows provision passwords ([0ad12cf](https://github.com/csautter/sailwright/commit/0ad12cf16475ba1b86ac0c2a5bf3029f9c2a0117))
* **provision:** set a working directory for the temporary Windows SSH account ([f44f0ee](https://github.com/csautter/sailwright/commit/f44f0ee41c31cfa70e53224140fc108905653e30))
* **provision:** set a working directory for the temporary Windows SSH account ([60fed8e](https://github.com/csautter/sailwright/commit/60fed8e8227ff2d01111f74b2b941ff7993537cd))
* require ansible-core 2.19 in dependency installers ([e514257](https://github.com/csautter/sailwright/commit/e5142570c5c9e8f2b9d5f74c1ca4d41fb77a7183))
* resolve parallel download hangs and UTM bookmark failures ([e038af5](https://github.com/csautter/sailwright/commit/e038af5ee45913b88efc7050974597efb2668899))
* **roles:** compare exact tool versions and keep one Toolbox install ([65c0506](https://github.com/csautter/sailwright/commit/65c0506aa7810dc5937bc2fb1f46dc8505ac0c0c))
* **roles:** make local setup repeatable on macOS and Linux ([288d0eb](https://github.com/csautter/sailwright/commit/288d0ebcb631077333b82186beaa6da460e49af3))
* **roles:** verify downloads and preserve Toolbox on failed upgrades ([2b33688](https://github.com/csautter/sailwright/commit/2b33688a5d98bd29eb45d3b335cda20e5e9ae693))
* **runner:** bound SDK retries and make post-write failures cleanup-safe ([dd9aa59](https://github.com/csautter/sailwright/commit/dd9aa59c85370779555d07ea35a0be661ac54e4c))
* **runner:** distinguish initial power state from provisioning failure ([f17ab53](https://github.com/csautter/sailwright/commit/f17ab5345bdae7e9924af0e68e9e86558261dc11))
* **runner:** handle broker rollout in provisioning checks ([73e7b3e](https://github.com/csautter/sailwright/commit/73e7b3e3807c0c0570830d6bfe52a288fb3e9df4))
* **runner:** handle dependency failures with bounded retries ([99caf73](https://github.com/csautter/sailwright/commit/99caf731c0c79e07c8422bf4cce64f0a322fc11c))
* **runner:** harden request_runner reliability and add runner_status ([7d522b0](https://github.com/csautter/sailwright/commit/7d522b0c17a511a327747c130ea0c28864d6c461))
* **runner:** isolate concurrent requests with unique resource names ([243bb19](https://github.com/csautter/sailwright/commit/243bb1932755d6b67c56d5e93604bd405a1b1095))
* **security:** address review findings after main rebase ([5ed677c](https://github.com/csautter/sailwright/commit/5ed677cfa9ff25d99eedf7b5a2a22700daad4b99))
* **selfupdate:** address risk hint and Python version review findings ([2c56e00](https://github.com/csautter/sailwright/commit/2c56e009ccd052ad116411972b1204bfc8488624))
* **selfupdate:** address update security and recovery review ([30d6d3d](https://github.com/csautter/sailwright/commit/30d6d3d56ebdfd2755ca3b9acd22aeba4e168408))
* **selfupdate:** copy backups when hard links are unsupported ([196b109](https://github.com/csautter/sailwright/commit/196b109c3f0e6e93802509b247715dc3f66b768c))
* **selfupdate:** make version comparison safe for invalid input ([970fd50](https://github.com/csautter/sailwright/commit/970fd50ba622ef8158912d4b145a40a91e3ca580))
* **selfupdate:** recover from invalid update settings ([92e33ab](https://github.com/csautter/sailwright/commit/92e33abeffee5cf5cbbc77c95dd805d6e50f6a86))
* **selfupdate:** restrict update source override to loopback ([7d17856](https://github.com/csautter/sailwright/commit/7d17856cc7312c146b3ca61c495a68d6e46d1d84))
* **selfupdate:** retry update checks that never completed ([61b08c4](https://github.com/csautter/sailwright/commit/61b08c4963913accd8f3f65b5f10f67f9b0baddf))
* **selfupdate:** wait briefly for pending update checks ([fcd0ccc](https://github.com/csautter/sailwright/commit/fcd0ccc905316afc6a9343a7cf78ab31c81072f2))
* **terraform:** require fresh reviewed plans for local apply ([8d1a58e](https://github.com/csautter/sailwright/commit/8d1a58e5cb76f1fa93ec7179b05bf196774e2ba5))
* **test:** accept ANSI-colored Ansible recaps, pass credential risk in vm-lifecycle, wait for lock in VM-22 ([395a653](https://github.com/csautter/sailwright/commit/395a653e7ed9d44d65d323a06b86aef59d1d6a48))
* **test:** add UTF-8 BOM to the Windows release kit script ([58bd4a2](https://github.com/csautter/sailwright/commit/58bd4a2d5077fa6c2d8cd262b4a4bda7aa0f7e01))
* **test:** add UTF-8 BOM to the Windows release kit script ([2712cc0](https://github.com/csautter/sailwright/commit/2712cc0a62c1707d5611c8abb61a240bb7de56ae))
* **test:** exclude process-filter helpers from cleanup ([4db600d](https://github.com/csautter/sailwright/commit/4db600d24d19716a12cca4d0ae488bef5dfb7531))
* **test:** gateway release checks for bridged UTM guests and pinned no-approvals start ([c3a85e1](https://github.com/csautter/sailwright/commit/c3a85e12a3d8d803f67ee1f6aa716ca0afe92772))
* **test:** keep the gateway agent dir outside the gateway state dir ([6796954](https://github.com/csautter/sailwright/commit/679695447544f8d016c9eeaa052c6eac6a7537fb))
* **test:** pass ShellCheck info-level findings in the release kit ([3ce4fcb](https://github.com/csautter/sailwright/commit/3ce4fcb7cf1db5da7d2e0001e7a9ef64cc77eb48))
* **test:** place gateway state outside the repository and keep evidence ([1a88ad4](https://github.com/csautter/sailwright/commit/1a88ad495788940c0425ed7213d21fc250fd49d0))
* **test:** restore terminal signals in PTY commands ([2784719](https://github.com/csautter/sailwright/commit/2784719954e26b395dd5674059d97b1a4182ad18))
* **test:** satisfy ShellCheck in the release kit launcher ([933ca28](https://github.com/csautter/sailwright/commit/933ca2841eff8e8fe846e2f6655aba992191e373))
* **test:** silence ShellCheck SC2163 for settings override export ([108f2ef](https://github.com/csautter/sailwright/commit/108f2ef20dbaefa9781209e281b8c61c1011bd13))
* **test:** tolerate slow SSH banner and skip-based refusal in gateway checks ([618e66a](https://github.com/csautter/sailwright/commit/618e66a82f29ad5af533050cd3e265e732abe230))
* **utm:** discover named instances in the configured bundle directory ([7438544](https://github.com/csautter/sailwright/commit/743854467a0d6e184fea3d3203ed5b138c973c62))
* **utm:** honor bundle directory across the VM lifecycle ([9ab635b](https://github.com/csautter/sailwright/commit/9ab635b09955ad686800b91107089a2793e5cd86))
* validate stable local provisioning on macOS and Linux ([c983bff](https://github.com/csautter/sailwright/commit/c983bffe06a806eeaaa3191030cd0d107a1565d2))
* **vm:** address named instance review findings ([4fb7fad](https://github.com/csautter/sailwright/commit/4fb7fad2159b0bddedef16570576059bf1c5966b))
* **vm:** coordinate image access across shared cache users ([784d5d3](https://github.com/csautter/sailwright/commit/784d5d36ea278f1a6ed818e02c04f8fb02bd3323))
* **vm:** integrate named instances with current listing and audit APIs ([5831ec8](https://github.com/csautter/sailwright/commit/5831ec85ebe8413cb40eabfeb212d58d56aeddaa))
* **vm:** preserve discovery and provisioning safeguards across hosts ([e4c23c2](https://github.com/csautter/sailwright/commit/e4c23c2640efd31453114f0f376a23d601e1e1b4))
* **vm:** resolve named instance security and CI findings ([4e216a2](https://github.com/csautter/sailwright/commit/4e216a2811805c3ea7ea7b325d76a1efb99c652a))


### Changed

* **build:** remove the unused in-place build path with artifact backups ([4cba123](https://github.com/csautter/sailwright/commit/4cba1235b8511a8046e2bb23cbb048cb4c45163b))
* **build:** share versioned download cache preparation ([16cb817](https://github.com/csautter/sailwright/commit/16cb8173c06641390057b98a9910523e552e4d6b))
* **gateway:** cache the merged secret list of derived redactors ([0ac8291](https://github.com/csautter/sailwright/commit/0ac8291242e065eeddefca7c84e4f0b1ecf81f9e))
* **gateway:** look up different VMs in parallel and share lookups per VM ([a89571c](https://github.com/csautter/sailwright/commit/a89571cd00a1807ca321abeaf852daaebec6518e))
* **gateway:** resolve VM targets in one place and split serveTunnel ([4b5bf66](https://github.com/csautter/sailwright/commit/4b5bf66e30805e033024a28f18a068cb24e9acaa))
* **selfupdate:** lower archive size limit to 128 MiB ([5e4ff0e](https://github.com/csautter/sailwright/commit/5e4ff0ee4270299c29562ad1355d9a6567144495))


### CI

* exercise clean Ansible installations on hosted runners ([bee5dfb](https://github.com/csautter/sailwright/commit/bee5dfbaf77ebe1d4a1dca11f0b4a87d720ff8ff))
* exercise installer-provided Ansible on Linux and macOS ([7170c6d](https://github.com/csautter/sailwright/commit/7170c6d52ca8e6036257613dbb48e1d58c52c966))
* **gateway:** exercise real guest access on disposable Linux runners ([f5233be](https://github.com/csautter/sailwright/commit/f5233beac6e98b7f713575f3155f6e9ad86679df))
* **gateway:** make guest runs opt-in and fix runner safety checks ([b61824b](https://github.com/csautter/sailwright/commit/b61824b10baaf7370aedefbc2e87844dfab12975))
* **gateway:** run guest checks automatically on relevant changes ([afb99f4](https://github.com/csautter/sailwright/commit/afb99f4060e75b2f7f7dad829da510e4b2be401e))
* **gateway:** test real Linux guest access on relevant PRs and main ([686cf16](https://github.com/csautter/sailwright/commit/686cf166516ce9a9b71d12dbbfe44cddba5ee66e))
* guard runner disk cleanup to hosted runners and keep apt clean CI-only ([5a2112c](https://github.com/csautter/sailwright/commit/5a2112cc9443754470d97f66c757b0668a9460af))
* **macos:** install xorriso for Linux profile checks ([b31b024](https://github.com/csautter/sailwright/commit/b31b0246558e96d2191d74ea7c0e39e73fc413f9))
* **macos:** separate Linux profiles, quick checks and scheduled builds ([1b5c9be](https://github.com/csautter/sailwright/commit/1b5c9be4367bfac8db818f45880ae6f364a3d5c9))
* **macos:** verify UTM guest readiness after full builds ([d555794](https://github.com/csautter/sailwright/commit/d55579405db096d6d10878dc1371be4feb3b4c35))
* note why the hosted Windows install job skips Hyper-V ([2a9bd51](https://github.com/csautter/sailwright/commit/2a9bd51f6665a21a6753b9b17cb60f3e00816656))
* reclaim runner disk space in linux build workflow ([ad4f5f2](https://github.com/csautter/sailwright/commit/ad4f5f20680453fc6e240db668baef7c351373f2))
* **release:** trust the throwaway Homebrew tap before linting formulae ([49b3e8b](https://github.com/csautter/sailwright/commit/49b3e8b43bfd1ee27fe111211645db3ac7f5c6f4))
* run all internal packages and trigger on provision changes ([51d5789](https://github.com/csautter/sailwright/commit/51d5789a80d96212cc96eec282d6d62ec5f75c4d))
* run guest security tests and track package changes ([2a19143](https://github.com/csautter/sailwright/commit/2a191435f9cb53082a3f838f0567d0aeebda9a85))
* run local provisioning through the CLI on macOS and Linux ([7595e2b](https://github.com/csautter/sailwright/commit/7595e2b232f316d1e9f8b00905a0c2460f0ec175))
* run the Ansible roles workflow only for local provisioning changes ([94f705e](https://github.com/csautter/sailwright/commit/94f705e3f4187e8dcb3979f85f814f6dd6eb29e3))
* run the oci unit tests with the race detector on Linux ([2646adc](https://github.com/csautter/sailwright/commit/2646adc3b95f2e96956208468ceb397bec9803f3))
* **runner:** fail fast on late provisioning failures via runner_status ([166a727](https://github.com/csautter/sailwright/commit/166a727ce20b8005d9dc3b978d81dcce102892a8))
* **self-update:** pin the Python interpreter for fixture tests ([dfa1aac](https://github.com/csautter/sailwright/commit/dfa1aaca21c2e0b985e1a8c71835f0cb5f36db7d))
* **self-update:** run updater unit tests on every pull request ([fc7aa2e](https://github.com/csautter/sailwright/commit/fc7aa2e9ec8bd77cfa2946c120230ff7646db244))
* share version checks and isolate the installer PATH ([d30490c](https://github.com/csautter/sailwright/commit/d30490ccb3561e6be2758f0520a4a33651f9a864))
* **terraform:** add credential-free checks and reviewed local plans ([a73add9](https://github.com/csautter/sailwright/commit/a73add95b843bc4680bdb08b573e0f2647bb0219))
* **terraform:** validate modules without Azure credentials ([afca3bb](https://github.com/csautter/sailwright/commit/afca3bb4bebe84a745f0352202e023d53007813c))
* **windows:** gate Azure Hyper-V builds behind routing and preflight ([aa2c6db](https://github.com/csautter/sailwright/commit/aa2c6db6437f51f3ff0a38a9485b82800b781337))
* **windows:** gate Azure Hyper-V builds behind routing and preflight ([c80c9dc](https://github.com/csautter/sailwright/commit/c80c9dcfdf0fefce800f1e82265c478955c37443))

## [0.26.0](https://github.com/csautter/sailwright/compare/v0.25.0...v0.26.0) (2026-11-03)


### Added

* **provision:** interactive provisioning picker, catalog and source management ([739a7a2](https://github.com/csautter/sailwright/commit/739a7a22d21379a78651f96b7d5ed78ec0cd8a53))


### Fixed

* **gh-runner-func:** build ARM request bodies from SDK models ([5635ff0](https://github.com/csautter/sailwright/commit/5635ff0a5e2e4f1dc2b84faca5876ccccfba250e))

## [0.25.0](https://github.com/csautter/sailwright/compare/v0.24.2...v0.25.0) (2026-11-02)


### Added

* **terraform:** harden state backend storage account ([cb93795](https://github.com/csautter/sailwright/commit/cb93795ef762bd6c9e35620a7faf951af95d4ab6))


### Fixed

* **gh-runner-func:** import ResourceManagementClient from new azure-mgmt-resource path ([ebb50a5](https://github.com/csautter/sailwright/commit/ebb50a56658848dbaad951a9a3bef6faed7e32b6))
* **gh-runner-func:** make delete_resource_group asynchronous and idempotent ([a06e838](https://github.com/csautter/sailwright/commit/a06e838dbfac9187e515e3ef4acd580592b34258))
* **make:** publish function app with --python flag ([07fa852](https://github.com/csautter/sailwright/commit/07fa852ad2f241077c0071fcd9737a57316f5faa))
* **provision:** fail when DEV_ALCHEMY_ROLE_SOURCES_CONFIG points at a missing file ([cb6c2bc](https://github.com/csautter/sailwright/commit/cb6c2bca7b887b58b6dd5453158b8befc441fa5a))
* **provision:** raise WinRM timeouts for local Windows provisioning ([9f3a9e3](https://github.com/csautter/sailwright/commit/9f3a9e3ed8596c04afcecff408378450faebdb07))
* **provision:** strip PowerShell 7 module paths before starting Windows PowerShell ([85c40e6](https://github.com/csautter/sailwright/commit/85c40e6bfeb39cb535c50821bc7b1c03084ffa76))
* **provision:** strip PowerShell 7 module paths before starting Windows PowerShell ([10f4617](https://github.com/csautter/sailwright/commit/10f46176c4bd96cb7e65657b73ea11bf83f7b6fa))
* **provision:** warn on ansible-core &lt; 2.19 with -K and cover --ask-become-pass end to end ([5f1a2ca](https://github.com/csautter/sailwright/commit/5f1a2ca528e7ec081a5bf71554fa7b65920f4d71))
* **provision:** warn when ansible-core hangs on a wrong become password ([775714b](https://github.com/csautter/sailwright/commit/775714bd22094a6e709535613b0262f44ea730a7))
* **runner:** truncate overlong output lines instead of aborting stream ([cf86e5e](https://github.com/csautter/sailwright/commit/cf86e5e46cfdb3f3b1afc91e811d1b71cd58f78e))
* **runner:** truncate overlong output lines instead of aborting stream ([203f35f](https://github.com/csautter/sailwright/commit/203f35fd3284db685411d3ba5fa26e6bd3e172a2))
* sanitize PSModulePath for every Windows PowerShell child process ([8c3a8c2](https://github.com/csautter/sailwright/commit/8c3a8c2b8d794fcb994fa76d61c8c87e1d154e11))
* **terraform:** enforce HTTPS on runner function app and document accepted trivy findings ([4673244](https://github.com/csautter/sailwright/commit/46732445b0e87168d48ad4277833b61c75109aa8))
* **terraform:** use per-unit trivy cache to avoid parallel bundle race ([15adcd1](https://github.com/csautter/sailwright/commit/15adcd178a275edc8813557a9e3bc37d8bf43fa1))
* **terraform:** use per-unit trivy cache to avoid parallel bundle race ([c3454a6](https://github.com/csautter/sailwright/commit/c3454a657762d117695e34523eb5c11ccd3ab770))


### Build

* **devcontainer:** install Azure Functions Core Tools ([f2d440c](https://github.com/csautter/sailwright/commit/f2d440cf970254d81d821b56111424e4f64a4695))


### CI

* **ask-become-pass:** pin python 3.13 on macos and check only the test sudoers file ([5b315a2](https://github.com/csautter/sailwright/commit/5b315a25aca12c7f8f1366871e4c1a494d50676b))
* confirm Hyper-V runner cleanup via delete-resource-group.sh ([4ea02e6](https://github.com/csautter/sailwright/commit/4ea02e6b09eb6299d805e5735f20677da79b6b19))
* run sailwright provision local --check from pwsh on a GitHub-hosted Windows runner ([89b3497](https://github.com/csautter/sailwright/commit/89b34979972042eb06bf1ef93d6d97af9202f803))
* run winpsenv tests and widen Windows PowerShell workflow triggers ([7dc940a](https://github.com/csautter/sailwright/commit/7dc940ac870a8489342293d7b3cbe19e01c9b292))

## [0.24.2](https://github.com/csautter/sailwright/compare/v0.24.1...v0.24.2) (2026-10-31)


### Fixed

* **install:** keep newer preinstalled Azure CLI and Python on Windows and test install on GitHub-hosted runners ([2f30b3c](https://github.com/csautter/sailwright/commit/2f30b3cd42fb3a06d02d690ec01484e6d57aaa33))
* **install:** keep newer preinstalled Azure CLI on Windows instead of downgrading ([0c93056](https://github.com/csautter/sailwright/commit/0c930566c347131c8d341b7a2643ac4ad54caebd))
* **install:** keep newer preinstalled native Python on Windows instead of downgrading ([e2855f4](https://github.com/csautter/sailwright/commit/e2855f46b14fef3b40c7c24c3bfaa2bb7e468168))
* libvirt guest clock sync ([b3665ae](https://github.com/csautter/sailwright/commit/b3665aee02a77c68002227a7e14c41a633925e1f))


### CI

* run sailwright install on a GitHub-hosted Windows runner ([c81cfbd](https://github.com/csautter/sailwright/commit/c81cfbd75ba53fb2d4313c649996a7a8e6b4d533))

## [0.24.1](https://github.com/csautter/sailwright/compare/v0.24.0...v0.24.1) (2026-10-31)


### Fixed

* **install:** compare Git for Windows build numbers against the pinned version ([5926a82](https://github.com/csautter/sailwright/commit/5926a820aaa57552bf09ff07fd06e7f29782cd95))
* **install:** keep newer preinstalled Git on Windows instead of downgrading ([27ddbb8](https://github.com/csautter/sailwright/commit/27ddbb89236ed17e8a77cfc8a292748418d03852))
* **install:** only treat an existing Git for Windows install as preinstalled Git ([69bffca](https://github.com/csautter/sailwright/commit/69bffcada55eb881db1a55dd6f4a9c7fad35f045))
* **install:** treat the pinned Git version as a minimum for any Git for Windows install ([b0feadb](https://github.com/csautter/sailwright/commit/b0feadb4d57ff6d3b4ae01827855a3cdd02e5d11))
* windows install ([143fed6](https://github.com/csautter/sailwright/commit/143fed667cade0b359771a00c81b7f67a1327f7f))

## [0.24.0](https://github.com/csautter/sailwright/compare/v0.23.0...v0.24.0) (2026-10-30)


### Added

* ask become pass ([ae61aee](https://github.com/csautter/sailwright/commit/ae61aee76775887c4b357947277019e6b5f767f4))

## [0.23.0](https://github.com/csautter/sailwright/compare/v0.22.1...v0.23.0) (2026-10-30)


### Added

* **build:** add SAILWRIGHT_PROJECT_DIR to override the project dir ([84bce53](https://github.com/csautter/sailwright/commit/84bce532f80af196e02e833b3a85c8e6885f044e))


### Fixed

* **build:** only treat a Sailwright checkout as the project dir ([c95595d](https://github.com/csautter/sailwright/commit/c95595d54151dae58897b23cb42d5ce0fbe178b4))
* project dir detection ([0c26ead](https://github.com/csautter/sailwright/commit/0c26ead8cf0e2ea8de6fcf71ab1eab49beac50a8))


### Changed

* **build:** rename the project dir lookup to findSailwrightCheckout ([5dcd811](https://github.com/csautter/sailwright/commit/5dcd811e7d7bec10310ed37014fc5136dcef6a37))

## [0.22.1](https://github.com/csautter/sailwright/compare/v0.22.0...v0.22.1) (2026-10-30)


### Fixed

* **build:** embed linux install scripts and go.mod in release binaries ([f65743e](https://github.com/csautter/sailwright/commit/f65743e94623fff7e37aace0b21a05ba5a94c4d4))
* embed linux install scripts ([b7015be](https://github.com/csautter/sailwright/commit/b7015be4fdf7644c86fdc1b4147777bb8be70c84))


### CI

* **release:** assert the Go toolchain from the packaged install ([47a834c](https://github.com/csautter/sailwright/commit/47a834c9a2bf3051a49df9a2dd154be7a306038e))
* **release:** run the packaged-binary install only when installer inputs change ([a5fad9d](https://github.com/csautter/sailwright/commit/a5fad9d4fa7ddebf47fd976e800217e299c190ed))
* **test:** trigger Go unit tests on changes to all embedded asset dirs ([664a20f](https://github.com/csautter/sailwright/commit/664a20fe563e8c74c06890f2a6007e8394e21046))

## [0.22.0](https://github.com/csautter/sailwright/compare/v0.21.0...v0.22.0) (2026-10-29)


### Added

* **build:** add --no-prebuilt opt-out and harden the prebuilt offer ([751bafd](https://github.com/csautter/sailwright/commit/751bafdad13535cce564064f1f64c69926730569))
* **build:** choose the ARM64 QEMU machine by what QEMU supports ([376fc22](https://github.com/csautter/sailwright/commit/376fc224acf913455ca778155ced641f1a698704))
* **build:** log and check the QEMU version for macOS ARM64 builds ([99ee9a1](https://github.com/csautter/sailwright/commit/99ee9a122859adbb392256716f8d37b122755e5e))
* **build:** offer to pull the prebuilt Ubuntu image before a local build ([5b64e6f](https://github.com/csautter/sailwright/commit/5b64e6f0db3d58a38b57b20bc2399ca0ed88bdb4))
* **deps:** sync the qemu-efi-aarch64 pin with a script and workflow ([1aa1132](https://github.com/csautter/sailwright/commit/1aa113246751fff66ff672a6fc126387416d03ec))


### Fixed

* **build:** look for Homebrew Python in the Windows 11 ISO shell fallback ([0bcc0b3](https://github.com/csautter/sailwright/commit/0bcc0b36918381a64863a07731ff9e28e4699802))
* **build:** pin the ARM64 QEMU machine to virt-10.0 on macOS ([b2e9e28](https://github.com/csautter/sailwright/commit/b2e9e28aa8ffc860cc73c6691a4025deda0742f1))
* **build:** require Python 3.10+ in the Windows 11 ISO shell fallback ([494365c](https://github.com/csautter/sailwright/commit/494365c52b7fba9dcdc12327de3a235fb6104644))
* **build:** stop macOS ARM64 builds early when QEMU is older than 10.0 ([c075f35](https://github.com/csautter/sailwright/commit/c075f35d00051dfbad7ff79acc2189a0ea87afd6))
* **build:** stop macOS ARM64 QEMU builds hanging in UEFI ([d94cdb4](https://github.com/csautter/sailwright/commit/d94cdb48bb8b21ab7710d1c59d01bbdc5df3868b))
* **cmd:** treat only terminals as interactive input ([0080636](https://github.com/csautter/sailwright/commit/008063648a0027750c27d9a0caebc7e9143e6eb0))
* **deploy:** clone the qcow2 image into UTM instead of copying it ([f899785](https://github.com/csautter/sailwright/commit/f899785a5d55158b3f34577b1072ca482f451c1b))
* **deploy:** clone the qcow2 image into UTM instead of copying it ([41bec78](https://github.com/csautter/sailwright/commit/41bec780b4c3d1d5d84398ee9be4efdfc044d9e2))
* **deps:** download the ARM64 firmware to a unique temp file ([2489075](https://github.com/csautter/sailwright/commit/2489075480ded418c2b967edeb8de5e0a5f9a8dd))
* **deps:** fail CI when the qemu-efi-aarch64 pin is only partly bumped ([12c1a75](https://github.com/csautter/sailwright/commit/12c1a75bba478a3ff4117baad81c64d4a703b03c))
* **deps:** find Homebrew Python on macOS and bound the version probe ([006f6d9](https://github.com/csautter/sailwright/commit/006f6d9d70d70ed802f1bd53b22011c2bc2cd583))
* **deps:** install Homebrew Python 3.13 with the macOS dependencies ([2f27c47](https://github.com/csautter/sailwright/commit/2f27c47e922fb7a94db2b0d7a26cb00b2bebf2ef))
* **deps:** leave Homebrew python@3.13 unpinned ([14ea7e0](https://github.com/csautter/sailwright/commit/14ea7e0a10d09d3500944cbc2e0775dc0bb79773))
* **deps:** pin qemu-efi-aarch64 instead of following trixie ([3219476](https://github.com/csautter/sailwright/commit/3219476504cb43d38444eb4d43d39fecee251577))
* **deps:** pin the qemu-efi-aarch64 snapshot timestamp to first_seen ([4002195](https://github.com/csautter/sailwright/commit/4002195f3824a6034c543f0eeee8861e547dab0a))
* **deps:** require Python 3.10+ for the Windows 11 download venv ([6944734](https://github.com/csautter/sailwright/commit/69447347b780e3a85408a56f380bafe838b3f7d5))
* **oci:** correct artifact pull commands and host labels ([92d8b44](https://github.com/csautter/sailwright/commit/92d8b445f5d281c86bb851759e7933901c43a402))
* **oci:** derive artifact host support from foreign pull rules ([c854265](https://github.com/csautter/sailwright/commit/c85426567356af31d8bdbc71c1f0b5f3b872db87))
* **oci:** describe sailwright pull and host support in artifact description ([dadfaab](https://github.com/csautter/sailwright/commit/dadfaab0a40e98230fd0a1e7e90cbf937ad7ad5f))
* refresh iso upload logging ([6184c51](https://github.com/csautter/sailwright/commit/6184c51a6acb8f8a78ecbac70e6d416da3581ad7))


### Changed

* **build:** drop the version parameter from qemuEfiAarch64URLs ([b20ffd7](https://github.com/csautter/sailwright/commit/b20ffd7287f348ddd200be6e18520310ed45dd36))
* **build:** share the qemu-efi-aarch64 dependency definition ([6a0bf21](https://github.com/csautter/sailwright/commit/6a0bf21356f0e13a98b1951da0538df273479569))
* **deps:** probe each Python interpreter once ([89be125](https://github.com/csautter/sailwright/commit/89be125f7b4f88feb2c7a59331b21144323e1ac0))


### Build

* **devcontainer:** add optional local hooks for machine-specific setup ([8da761a](https://github.com/csautter/sailwright/commit/8da761a39930f86152c3c66abc0960cab95539bb))
* **devcontainer:** mount host codex dir for auth and drop extra agent mounts ([da006e0](https://github.com/csautter/sailwright/commit/da006e03e33cfb94ecd589e92072a31e74b49074))
* **devcontainer:** run the host hook from outside the workspace ([6ebf6b4](https://github.com/csautter/sailwright/commit/6ebf6b44ff09569e04fbbcd01f4ebc829a64a1d6))
* **devcontainer:** update go base image to 2.3.3 ([2274ccc](https://github.com/csautter/sailwright/commit/2274ccc4eb19d9b5dc8ac5f071309c5428bbee83))
* **go:** pin the Go toolchain in go.mod and let Renovate bump it ([88eac04](https://github.com/csautter/sailwright/commit/88eac04f829b3ba4ebed5f122bab0112b043a206))
* **go:** pin the Go toolchain in go.mod and let Renovate bump it ([ced4e5b](https://github.com/csautter/sailwright/commit/ced4e5b34b4ddfc9cf49fb32d177f08a54e44ef1))


### CI

* pin the published image contract and widen pull test triggers ([0e33b1d](https://github.com/csautter/sailwright/commit/0e33b1df6afbec4d8892e3eab4c478595bc41d0a))

## [0.21.0](https://github.com/csautter/sailwright/compare/v0.20.0...v0.21.0) (2026-10-26)


### Added

* install scripts ([c85a231](https://github.com/csautter/sailwright/commit/c85a231eedfb81bbcefed9c9db07ac1033562b51))
* **install:** add one-line install scripts for macOS, Linux and Windows ([99d8a23](https://github.com/csautter/sailwright/commit/99d8a232b44629364a76d5c5ed969dae3d9cac43))


### Fixed

* **install:** guard install.sh against truncated pipes ([ff356ce](https://github.com/csautter/sailwright/commit/ff356ce7b401442772dd24cd6005d41628a36510))
* **install:** keep install.ps1 from changing the caller's session ([399eede](https://github.com/csautter/sailwright/commit/399eede79245cb27720c1270b423d8c794abf8a0))
* **install:** keep install.ps1 parameters out of the caller's session ([be1d30e](https://github.com/csautter/sailwright/commit/be1d30e130f38231fc3fce7cc1371c1879f5bcaf))
* **install:** keep the CLI and gateway on one version after failed updates ([eb878f8](https://github.com/csautter/sailwright/commit/eb878f8a44974d34fe534bc8ae5cb7756822848d))
* **install:** preserve existing binaries during updates ([89e3945](https://github.com/csautter/sailwright/commit/89e39453a7582e8e6cf215f42683c952c49b5d56))


### CI

* **install:** broaden install script tests and lint install.ps1 ([4691828](https://github.com/csautter/sailwright/commit/4691828ffa6602f052a17e32fa83eb8755cb5ba5))
* **release:** check the install scripts against each PR's archives ([b88d3b1](https://github.com/csautter/sailwright/commit/b88d3b1db2cae070f940eb4c29da1eb8db17dae6))
* **release:** publish from main only and verify the install scripts ([e9dbc85](https://github.com/csautter/sailwright/commit/e9dbc85b0b5191f4607b6c2e948162356118c07a))

## [0.20.0](https://github.com/csautter/sailwright/compare/v0.19.1...v0.20.0) (2026-10-26)


### Added

* **release:** publish homebrew formulae to a tap on every release ([5916183](https://github.com/csautter/sailwright/commit/5916183d2ba403019592504426083a9008cb4518))
* **release:** publish Homebrew formulae to a tap on every release ([66f894b](https://github.com/csautter/sailwright/commit/66f894b7491a4dd617944803b26bf4cf2f2984e2))


### Fixed

* **build:** preserve windows iso identity and object immutability ([6cab5e5](https://github.com/csautter/sailwright/commit/6cab5e5110f6c9f223502002f198d4b05f00d9a0))
* **build:** recognize windows11_client iso names ([3af8ef6](https://github.com/csautter/sailwright/commit/3af8ef625271e29aee515c7e821133c5c45c7e91))
* **build:** recreate unusable windows 11 download venv ([7a58c18](https://github.com/csautter/sailwright/commit/7a58c185cf41c51f4360725b2d3a9651ebde0c41))
* **build:** require packer 1.14 for official plugin installs ([a369f7e](https://github.com/csautter/sailwright/commit/a369f7ed91ea6a17e5f05491fd670c937d5a2dc8))
* **build:** restrict windows iso checksum lookup to the us row ([2227ed0](https://github.com/csautter/sailwright/commit/2227ed0b2897efd35b0e0889c361bba88bc1358d))
* **build:** run packer init once and retry network errors ([5a1de41](https://github.com/csautter/sailwright/commit/5a1de41917c9c20e7409bddeefe9272cf634dd8f))
* **cache:** annotate manifest references to missing iso objects ([93760cb](https://github.com/csautter/sailwright/commit/93760cb802fa5d56e8fbea9a152975790f83b876))
* **cache:** enforce manifest authority and repair missing iso objects ([38f458d](https://github.com/csautter/sailwright/commit/38f458dcb106b757798713d3074ffcf9c08dc134))
* **cache:** reset exit code after tolerated ftp cache misses ([43b4f96](https://github.com/csautter/sailwright/commit/43b4f96ecefba62c566e47b96d3deb3337632778))
* **cache:** treat missing ftp directories as cache misses ([2fc4a4f](https://github.com/csautter/sailwright/commit/2fc4a4fbe307c25a8767600aa6479c2e2b3d4c85))
* **deploy:** wait longer for hyper-v guest ip ([05d86b4](https://github.com/csautter/sailwright/commit/05d86b4d185dfd7e3e54d060ac7482043cf27393))
* **devcontainer:** support WSL bash on Windows hosts ([c3f4353](https://github.com/csautter/sailwright/commit/c3f435385c58033f96cd9128c33c9cdb37cfe430))
* **gateway:** pass COMPUTERNAME to windows child processes ([b0d93dd](https://github.com/csautter/sailwright/commit/b0d93dd4387b3e25592fcb8e78a64f53b069ed60))
* linux ubuntu hyperv vagrant ([c5af237](https://github.com/csautter/sailwright/commit/c5af2371713f5ab414802e6f04df8b08ef6605d0))
* **provision:** check sshpass in cygwin on windows hosts ([0fe58bf](https://github.com/csautter/sailwright/commit/0fe58bff5f814484de4d67f68bc0ffdea2d5d742))
* **provision:** default windows guest winrm credentials ([167e7bc](https://github.com/csautter/sailwright/commit/167e7bca188e5ad4603ab5d01d53768050705ecb))
* **release:** harden homebrew tap publishing ([377f786](https://github.com/csautter/sailwright/commit/377f7864a01ecc3c86c6a8adb8a93209d832d665))


### Build

* **devcontainer:** color each clone's vs code window by its name suffix ([cf1855b](https://github.com/csautter/sailwright/commit/cf1855ba42349de1f7534391407fd2c30b5815f1))
* **devcontainer:** install codex cli and cache its plugin dir in a volume ([34c3e5a](https://github.com/csautter/sailwright/commit/34c3e5acc1d83fa15738e84d23429ca0bf67be75))
* **devcontainer:** keep codex runtime tmp dir in a docker volume ([43cd172](https://github.com/csautter/sailwright/commit/43cd17269205d42350c7678a9fefda251bde2b85))
* **devcontainer:** persist codex and claude code state across rebuilds ([c19a6db](https://github.com/csautter/sailwright/commit/c19a6db188013a9fe9c84102f69b5cff96db56d7))
* **devcontainer:** reinstall sailwright-gateway on every start ([0a73172](https://github.com/csautter/sailwright/commit/0a7317229d01b535ec748a5149af30a56c09d8a0))


### CI

* **e2e:** remove lifecycle runs from build workflows ([c409b86](https://github.com/csautter/sailwright/commit/c409b86ca79224bf830941471caf60924dc337ff))
* **e2e:** run lifecycle scenarios using built images ([911b85b](https://github.com/csautter/sailwright/commit/911b85b017d6dbcb2aaecdec7dc422504081bf34))

## [0.19.1](https://github.com/csautter/sailwright/compare/v0.19.0...v0.19.1) (2026-10-23)


### Fixed

* **build:** do not fail the build when virtio-win mirror probes time out ([570a2aa](https://github.com/csautter/sailwright/commit/570a2aa191425d50911636aa1baed0d088921052))


### CI

* **release:** publish sailwright-gateway binaries as release assets ([176be10](https://github.com/csautter/sailwright/commit/176be10fe9c6e9561ccfd16c0bed319746b950bf))
* **release:** publish sailwright-gateway binaries as release assets ([5e1b674](https://github.com/csautter/sailwright/commit/5e1b67449d45478a88e023c414899d7c78833937))

## [0.19.0](https://github.com/csautter/sailwright/compare/v0.18.0...v0.19.0) (2026-10-23)


### ⚠ BREAKING CHANGES

* **gateway:** sailwright-gateway no longer listens on TCP by default on Linux; agents use the agent socket. Set "listen: 127.0.0.1:7391" to restore TCP.

### Added

* agent test gateway ([8b1d7aa](https://github.com/csautter/sailwright/commit/8b1d7aa14c5f72bec32e339a9c17be04aaf65b33))
* **build:** log download progress when stdout is not a terminal ([f560bfa](https://github.com/csautter/sailwright/commit/f560bfaafcf7fb7e367ef9ff48ba8b85c972fee4))
* **cli:** add --json output to list subcommands ([e9a4ed4](https://github.com/csautter/sailwright/commit/e9a4ed4c7f92e4474e0ae059a32b52754dd51bab))
* **gateway:** add agent Unix socket and stdio connect bridge ([603b284](https://github.com/csautter/sailwright/commit/603b284a09804782f0c985afc10dab5e163b8813))
* **gateway:** add sailwright-gateway for agent access from devcontainers ([137edd2](https://github.com/csautter/sailwright/commit/137edd26a18430053fd981d4af6d5822a5517c25))
* **gateway:** allow disabling the TCP listener and default it off on Linux ([524ec94](https://github.com/csautter/sailwright/commit/524ec9401b6676f8af8c6a306384dba1929d0719))
* **gateway:** approve local provisioning with a sudo password ([417371c](https://github.com/csautter/sailwright/commit/417371c5c6faadfad619786a699bcfd62e4e18be))
* **gateway:** audit start and stop, expose limits in gateway_info ([6e94c60](https://github.com/csautter/sailwright/commit/6e94c602cac84014528ffee01b5a6c04ceac0320))
* **gateway:** cap and prune job logs, summarize background refresh audit ([662d0ea](https://github.com/csautter/sailwright/commit/662d0eafe8c0e5a2d642415ec6b14466818263f9))
* **gateway:** hint at the listen setting in the banner when TCP is off ([9ccfbe2](https://github.com/csautter/sailwright/commit/9ccfbe27f9415510a761c37865b2e4d8f33213cf))
* **gateway:** offer to delete the cache on shutdown ([eb7b4fd](https://github.com/csautter/sailwright/commit/eb7b4fdde69c68becdbfe26660af5c6f7840b80f))
* **gateway:** show the build console to agents and on the host ([33e9ccc](https://github.com/csautter/sailwright/commit/33e9ccc2154bfc078e081c64c0b01212d42fac60))
* **gateway:** show the job log path and add --follow ([72599cf](https://github.com/csautter/sailwright/commit/72599cf6f0986db19a6b9c5d7fbbdfbd380ecfde))
* **runners:** build Azure runner golden image in GitHub Actions ([958abfa](https://github.com/csautter/sailwright/commit/958abfa6c2965736341e60d55af4c46b3e04628f))
* **terraform:** add build_cache_enabled switch for legacy Azure build cache ([837700a](https://github.com/csautter/sailwright/commit/837700ac818ccf2177772c4a88619912f07f82c0))
* **terraform:** add image builder identity and resource groups for golden image CI ([cda9923](https://github.com/csautter/sailwright/commit/cda9923a6ac0150893c462f59a951c4ed442a8a6))


### Fixed

* **build:** do not stop on terminal hand-off when running in the background ([b4bf288](https://github.com/csautter/sailwright/commit/b4bf28872fa9e26bf14fa3b5bc28ccb728842d45))
* **build:** keep the qemu packer output dir in the cache ([55d8a6b](https://github.com/csautter/sailwright/commit/55d8a6ba32546ae31cc0ad7973e655f440034bd4))
* **build:** remove QEMU Packer scratch output after failed builds ([a8f4f04](https://github.com/csautter/sailwright/commit/a8f4f04b77ba3bb234f0e9ab2800837fae680a22))
* **build:** retry web file downloads on checksum mismatch ([f771d35](https://github.com/csautter/sailwright/commit/f771d35abf492d02334e2332aacae19d174af42e))
* **cache:** treat FTP build cache upload failures as warnings ([e7baba8](https://github.com/csautter/sailwright/commit/e7baba8793506f96b20355fe0b5b59cd4c2fa916))
* **ci:** grant id-token permission to release-binaries caller ([856c107](https://github.com/csautter/sailwright/commit/856c1079c1f2fc82a3e8012d323b620c23680389))
* **ci:** grant id-token permission to release-binaries caller ([4c2a89f](https://github.com/csautter/sailwright/commit/4c2a89f3e3fa3463967bbae037377777804a4f33))
* **cli:** exit non-zero when a build fails ([92adb70](https://github.com/csautter/sailwright/commit/92adb70a60552338fb5d49cd873a66f82a97c139))
* **deps:** sync virtio-win pins and automate checksum updates ([7b9b133](https://github.com/csautter/sailwright/commit/7b9b133c1026a16713eac34a6282e039613454f3))
* **devcontainer:** auto-install declared VS Code extensions on start ([5b289ef](https://github.com/csautter/sailwright/commit/5b289ef059809406cd456cc99b109fcb52500f34))
* **devcontainer:** satisfy shellcheck SC2088 in initialize-host.sh ([e204149](https://github.com/csautter/sailwright/commit/e204149115c0563866850bd7e675ad5aa86b534a))
* **gateway:** address gosec findings ([b7369de](https://github.com/csautter/sailwright/commit/b7369de6824631c872c29c999bc872d7892a9019))
* **gateway:** bind before printing the listening banner ([9874287](https://github.com/csautter/sailwright/commit/98742878e3fa432179b6e592d60b2ed617cfebbc))
* **gateway:** do not fail jobs when the process tree cannot be attached ([ebb6474](https://github.com/csautter/sailwright/commit/ebb6474a5355ca8e56a00c00fe0dbe2a97ea291b))
* **gateway:** do not offer the sudo password with a custom inventory ([0b22de5](https://github.com/csautter/sailwright/commit/0b22de53d6ff869eeaa0ee5d527bd8b90f539153))
* **gateway:** harden shared cache check, become detection and redaction ([9a49921](https://github.com/csautter/sailwright/commit/9a49921c0dafc86806690f30e106dfb971e2f8e8))
* **gateway:** harden snapshot copy and approval console ([79e21f3](https://github.com/csautter/sailwright/commit/79e21f3e12c47455fb738f11fac700387822195e))
* **gateway:** hold an exclusive state-dir lock while the gateway runs ([ca97585](https://github.com/csautter/sailwright/commit/ca9758596ea543e5d4cc4c3d0d8f694f7b5e481a))
* **gateway:** keep large data in the platform data dir ([b948587](https://github.com/csautter/sailwright/commit/b948587753751b151cab9a169166f62e2d4cdc35))
* **gateway:** name failed listings in the audit and keep redactor limits on With ([7ebd406](https://github.com/csautter/sailwright/commit/7ebd4063de165240c57366f3e5a1b18ec283682f))
* **gateway:** never split a complete secret at the forced redaction flush ([47289eb](https://github.com/csautter/sailwright/commit/47289ebf3c6998a1f07d93c0ad34a061928817c8))
* **gateway:** open bundle_root through the workspace root when snapshotting ([a18cb08](https://github.com/csautter/sailwright/commit/a18cb08d337023b19679d6a7f0dbfc2f1ade648d))
* **gateway:** redact short sudo passwords and per-job secrets in job errors ([9e20b7d](https://github.com/csautter/sailwright/commit/9e20b7dfae83761e506bf2184d260bbc97b8f7b2))
* **gateway:** resolve bundle_root symlinks and canonicalize path ([fd930a8](https://github.com/csautter/sailwright/commit/fd930a842084adb4d9fbcbde82638218f8774745))
* **gateway:** run jobs in their own session without a terminal ([c0609c4](https://github.com/csautter/sailwright/commit/c0609c44fcc2a465b8536d62d085e7a1c9d02aab))
* **gateway:** use escapes for invisible characters and annotate gosec findings ([fe9f9f3](https://github.com/csautter/sailwright/commit/fe9f9f3adf8038119a01a89f8c40de83c8c000d7))
* **gateway:** wait for jobs and background refreshes on shutdown ([aeeb89b](https://github.com/csautter/sailwright/commit/aeeb89b0c3ebec2281dae47805453ab26b851689))
* **gateway:** warn before approve-sudo when args can redirect the play ([c9d6b0d](https://github.com/csautter/sailwright/commit/c9d6b0dad00a0051564e1022cb7c2586a1d7cd04))
* **make:** fail gosec target when packages are not analyzed ([60571b9](https://github.com/csautter/sailwright/commit/60571b95454df2127f04110539d1d118896b340b))
* **packer:** remove scratch output after failed builds under set -e ([fa679c3](https://github.com/csautter/sailwright/commit/fa679c3ffea0710c66daeac47beabe083a9a42a6))
* **runners:** store Azure golden image on Standard HDD and skip unused VirtualBox image ([693671f](https://github.com/csautter/sailwright/commit/693671f256d9bd33b1d6d7f7b6efc0ad6a80a679))
* **ubuntu:** use new cdimage path for arm64 live server ISO ([eb37d53](https://github.com/csautter/sailwright/commit/eb37d531bf2a8267b348f8546e22098d15eda5fe))
* **ubuntu:** use new cdimage path for arm64 live server ISO ([747ecd2](https://github.com/csautter/sailwright/commit/747ecd2e2e63191bd3634243a9acf887aebad8c9))
* **win11:** fail fast with diagnostics when resolving the ISO link ([31c13fc](https://github.com/csautter/sailwright/commit/31c13fc50a1c3c5d6060bbc72624235c8d54a8cb))


### Changed

* **devcontainer:** generate host mounts via compose override ([44eeeaa](https://github.com/csautter/sailwright/commit/44eeeaaf9565c0bbed98e6cbc5ab4fbd2d0c037f))
* **gateway:** amortize job output buffer compaction ([6d92910](https://github.com/csautter/sailwright/commit/6d9291095b6e6120e71bdfce928e9cac4b2838e0))
* **gateway:** move output buffering and become helpers out of jobs.go ([8e0c3b2](https://github.com/csautter/sailwright/commit/8e0c3b2f9cc3060288ebc2ed07b374a5592e71c6))
* **test:** split static file checks and parse workflows as YAML ([a70c77e](https://github.com/csautter/sailwright/commit/a70c77ecab1a368e232a3f56730b51ec4ae7f6c9))
* **test:** split static file checks and parse workflows as YAML ([dc397b0](https://github.com/csautter/sailwright/commit/dc397b0b3718e2a0dde159320c2f56a16a31f58b))


### CI

* **linux:** run cold-cache bootstrap nightly and on relevant PRs ([b0db6f5](https://github.com/csautter/sailwright/commit/b0db6f52300d1b4b1e3447da0e54af7505727d8b))
* remove Hetzner self-hosted runner and S3 cache backend ([6a87023](https://github.com/csautter/sailwright/commit/6a87023b533e15a866d9e63148f02b955eb74611))
* remove Hetzner self-hosted runner and S3 cache backend ([1f2401c](https://github.com/csautter/sailwright/commit/1f2401cdd8e770e2d7c0ecedef209c002f3a5b64))
* run gateway tests and the E2E smoke scenario on all host OSes ([8a541b8](https://github.com/csautter/sailwright/commit/8a541b8c52d88d79130a9bc8d4b7ca7a6a2c76af))
* **test:** run gateway and E2E runner tests with -race on Linux ([5d35313](https://github.com/csautter/sailwright/commit/5d353134c072c53de10927d379db15872c214b05))


### Documentation

* **gateway:** document the Linux listen default, devcontainer prerequisites and ADR status ([a104f4f](https://github.com/csautter/sailwright/commit/a104f4f90dcbb3c9d6f489484e123969bb070bef))

## [0.18.0](https://github.com/csautter/sailwright/compare/v0.17.0...v0.18.0) (2026-07-23)


### Added

* created automation to generate example videos and gifs ([4f36e68](https://github.com/csautter/sailwright/commit/4f36e68d3ae859351d5b4450c90d70979c2d67eb))
* **demo:** add side-by-side VM-build video and quickstart --no-cache ([8f03cc7](https://github.com/csautter/sailwright/commit/8f03cc7bd67751550070e44137ec8bd15762dc8c))
* **demo:** add typed terminal demo for website and README ([8dabbc8](https://github.com/csautter/sailwright/commit/8dabbc8a09b376417d9363f293fec410f17202f6))
* **quickstart:** add recorded end-to-end quick-start automation ([02c990f](https://github.com/csautter/sailwright/commit/02c990f6c3d41cc2738e4e59e294bada6e5858ff))


### Fixed

* **demo:** guard empty trim_args expansion under set -u ([1f4f45a](https://github.com/csautter/sailwright/commit/1f4f45a0f036e921c823b14651f9d7e672927f2c))
* **quickstart:** harden recording collection and VNC capture ([46e7b25](https://github.com/csautter/sailwright/commit/46e7b25dc32908f6dfc0ba1313b36ba95efea09f))
* **quickstart:** propagate recorded command's exit code through asciinema ([6c6cb60](https://github.com/csautter/sailwright/commit/6c6cb60cfe101b30549552142520f57c718b85b6))

## [0.17.0](https://github.com/csautter/sailwright/compare/v0.16.1...v0.17.0) (2026-07-18)


### Added

* make role-sources-test.yml the default provision playbook ([41836f0](https://github.com/csautter/sailwright/commit/41836f0e1e8532f73c32643d19d50b8b185a0a7a))


### Fixed

* **terraform:** replace deprecated tflint module config key ([fa2d978](https://github.com/csautter/sailwright/commit/fa2d978f2a1889562df9de014ce6d9afaa4c58a7))


### Changed

* **rename:** rename user-facing surfaces to sailwright ([a057220](https://github.com/csautter/sailwright/commit/a0572202e436fe551eb1ae637c093a1f997da0b5))


### Build

* **devcontainer:** install azure cli ([02b73b2](https://github.com/csautter/sailwright/commit/02b73b2902d6675ccf78546aea69c2bc50020bc4))
* **devcontainer:** install trivy and refresh terraform lockfile ([1cb9c74](https://github.com/csautter/sailwright/commit/1cb9c74a97e64673fe6708ffc5c99bee98e11989))


### CI

* **runners:** point macos tart runner at sailwright ([e80ebd7](https://github.com/csautter/sailwright/commit/e80ebd73c5d3085c74bcc191f42999dca00a6de6))

## [0.16.1](https://github.com/csautter/dev-alchemy/compare/v0.16.0...v0.16.1) (2026-05-17)


### Added

* add build features for linux ubuntu hosts ([911ed61](https://github.com/csautter/dev-alchemy/commit/911ed613421020d6ed28a920bf72bbf10e4cda0e))
* add oci registry support ([9615e78](https://github.com/csautter/dev-alchemy/commit/9615e7848908c0c44cb34eeb3db126eb92e89c98))
* add windows builds an vms for linux hosts ([eb7f440](https://github.com/csautter/dev-alchemy/commit/eb7f4401f2a15f3f6ba1220beb79a967515bd9cb))
* added linux create vm command ([931cae0](https://github.com/csautter/dev-alchemy/commit/931cae0c1a13c68c9a3c9ee5a527abc220faffb3))
* added windows build on windows to go wrapper ([6d87a37](https://github.com/csautter/dev-alchemy/commit/6d87a3762e25215a326a0a6a0c8d8563143f4d48))
* ansible role source configurable ([79c6cd7](https://github.com/csautter/dev-alchemy/commit/79c6cd7c7a2369432605afb62fcdacd8a14c1157))
* **ansible:** add configurable Debian Spotify install with apt/snap fallback ([c5ae866](https://github.com/csautter/dev-alchemy/commit/c5ae866242d7c378f5f054e10a983a25f5c84b80))
* **build:** add --no-cache to force rebuilding existing VM artifacts ([13c1783](https://github.com/csautter/dev-alchemy/commit/13c17833328fb144700dc9193552509b71ebd0a2))
* **build:** add Hyper-V Ubuntu support on Windows and restructure cloud-init configs ([eaef86d](https://github.com/csautter/dev-alchemy/commit/eaef86df0f0c57f87fe8830c867a92e536b9ca52))
* **build:** add Hyper-V Ubuntu support on Windows and restructure cloud-init configs ([f99f622](https://github.com/csautter/dev-alchemy/commit/f99f62293c4f02e4c69df7046134d0713b6ba729))
* **build:** add Linux QEMU support for Windows 11 guests ([36097fc](https://github.com/csautter/dev-alchemy/commit/36097fcc1600b3cb1795f8dc35d7c10fc2566db7))
* **build:** add Linux-host QEMU builds for Ubuntu server and desktop ([364d308](https://github.com/csautter/dev-alchemy/commit/364d308b03423117b5488d434643036b74f7fcfa))
* **build:** add list command for available VM build combinations ([023d67f](https://github.com/csautter/dev-alchemy/commit/023d67f9673905b469b03cebe6402d3a2ecebe67))
* **build:** add verbose flag for packer builds ([c4a8c63](https://github.com/csautter/dev-alchemy/commit/c4a8c63f044d17baedcec533c777341e788b40ba))
* **build:** enable VNC video capture for Linux host builds ([3d65997](https://github.com/csautter/dev-alchemy/commit/3d65997b6bd4b16a03af250f688d1a69bb0c3241))
* **build:** select fastest virtio-win download source ([3d0d40f](https://github.com/csautter/dev-alchemy/commit/3d0d40f435028cbee2adb9b56a272e148439fa80))
* **build:** show artifact status in build list ([64bb53a](https://github.com/csautter/dev-alchemy/commit/64bb53a2bf62760674f7dba97d9307c9c19a30dc))
* **build:** stream VNC snapshots directly to mp4 ([0cc1a7d](https://github.com/csautter/dev-alchemy/commit/0cc1a7de50fbfcd18cc94496fb96b6c84a14ed32))
* **ci:** add FTPS build cache backend ([2d0884b](https://github.com/csautter/dev-alchemy/commit/2d0884b804e32c345826b371a08d6d5fee333f33))
* **ci:** add Hetzner S3 build cache backend ([d13c57b](https://github.com/csautter/dev-alchemy/commit/d13c57b2dc17b508092fae5b6df10432c969c02f))
* **ci:** add in-repo Hetzner self-hosted runner workflow for Linux builds ([fc88683](https://github.com/csautter/dev-alchemy/commit/fc88683f31dec72a868b7410ba862deb7a5a8fe0))
* **cli:** add destroy command for managed VMs ([490901b](https://github.com/csautter/dev-alchemy/commit/490901bea7816f307a477889b29ff8017ac2ae53))
* **cli:** add install command for host dependencies ([0ffd087](https://github.com/csautter/dev-alchemy/commit/0ffd0877a9f9dec95a39d298f4cb26844849c61d))
* **cli:** add optional Go install flag and Linux host installer ([44bebc0](https://github.com/csautter/dev-alchemy/commit/44bebc0b74059657d965b58dbaae338f114c43f2))
* **create:** add create list command and artifact readiness checks ([1fc0bed](https://github.com/csautter/dev-alchemy/commit/1fc0bedfd180ae9b33957d57fea5982892f1d157))
* **create:** detect existing VM targets before deploy ([5ab8087](https://github.com/csautter/dev-alchemy/commit/5ab808707132864dd736b0f6722c25371d7ec334))
* **deploy:** add libvirt workflow for Ubuntu QEMU VMs on Linux ([44fcda8](https://github.com/csautter/dev-alchemy/commit/44fcda89b0c39d61d8b3d1032f0bf2cdc957948e))
* **deploy:** add Windows Hyper-V create flow and unify deploy command execution ([8e2836c](https://github.com/csautter/dev-alchemy/commit/8e2836c4e25630a0233a08da8770487ae5972f59))
* **deploy:** add Windows Hyper-V vagrant deploy and unify deploy command runner ([0a3f7be](https://github.com/csautter/dev-alchemy/commit/0a3f7bed39a68b1e849ee78bcf4a667fb5441adf))
* **destroy:** add VM destroy readiness listing and tart state fixes ([05e3175](https://github.com/csautter/dev-alchemy/commit/05e31751954c7905c98045fd6971eda461eb9686))
* embed runtime assets for standalone CLI builds and release binaries ([b8d7506](https://github.com/csautter/dev-alchemy/commit/b8d7506db767d1f1ce00cc01cf0981329869b43c))
* embed runtime assets for standalone CLI builds and release binaries ([af5d159](https://github.com/csautter/dev-alchemy/commit/af5d159650060283e88d6d6ae908d359b2e1421c))
* enhaned debugging for hyperv runner ([90ce346](https://github.com/csautter/dev-alchemy/commit/90ce3464f342f01a66b988f0f54c856aac991ef2))
* gh actions cache - add hetzner s3 and ftp ([7080cc0](https://github.com/csautter/dev-alchemy/commit/7080cc03f6a73680d57bc2f398c4e6bb4608e578))
* **hyperv:** parameterize Vagrant VM resources from build config ([ec52f11](https://github.com/csautter/dev-alchemy/commit/ec52f113796b11bb63be6751139f5378dd165a1c))
* **oci:** add push and pull list commands ([49ddd64](https://github.com/csautter/dev-alchemy/commit/49ddd64cf8ad84bbf5a1e35fedb31754f328b10e))
* **oci:** add registry push and pull for build artifacts ([3c061f2](https://github.com/csautter/dev-alchemy/commit/3c061f22a10fb3ee223f8f11b69174ec7ce5c3bb))
* **oci:** add TLS options for registry transfers ([e087def](https://github.com/csautter/dev-alchemy/commit/e087defa2600f4bc20ccdfc7e8a154c3b531eb66))
* **oci:** allow confirmed darwin/linux foreign artifact pulls ([a4786a1](https://github.com/csautter/dev-alchemy/commit/a4786a1ff7cbf594d175bcbaae532a9ff79fcad2))
* **oci:** show transfer progress for push and pull ([2e649a0](https://github.com/csautter/dev-alchemy/commit/2e649a0a563408d04563e1dcd98d41a5fca9845f))
* provision local windows ssh provisioning ([5115d6a](https://github.com/csautter/dev-alchemy/commit/5115d6ae30f7ae163f5922704360d54f906e1b69))
* **provision:** add generic ansible arg passthrough and inventory override ([0d2f284](https://github.com/csautter/dev-alchemy/commit/0d2f2842f3d4b09aa6935399c4b5ec34296f73a8))
* **provision:** add local host provision target ([833e077](https://github.com/csautter/dev-alchemy/commit/833e0776371e1beb42cff2432aee2b38208d3f79))
* **provision:** add local host provisioning with Windows WinRM bootstrap ([521aa46](https://github.com/csautter/dev-alchemy/commit/521aa464c5be6c6c012f8750a3ce879da495751f))
* **provision:** add local Windows SSH provisioning with force uninstall cleanup ([f11ad4b](https://github.com/csautter/dev-alchemy/commit/f11ad4bb985f4cda02ca2ced5b76e610f4234346))
* **provision:** add supported target listing and filter unsupported VMs ([f378c06](https://github.com/csautter/dev-alchemy/commit/f378c064aa14e8cc1a148237ef1ce5b1ee8a50d1))
* **provision:** add Ubuntu UTM provisioning support on macOS ([33dace4](https://github.com/csautter/dev-alchemy/commit/33dace45b6b2e9b32251c32a29f5cf97f8308884))
* **provision:** add Ubuntu UTM provisioning support on macOS ([cc83f38](https://github.com/csautter/dev-alchemy/commit/cc83f381030f2e95fd42a10894faa98375bbee0f))
* **provision:** add unified VM provision command for Hyper-V Windows ([f1063ba](https://github.com/csautter/dev-alchemy/commit/f1063bade3e5c5b85da8d250ed75192465dd3a0b))
* **provision:** add unified VM provision command with Hyper-V Windows flow via Cygwin ([177c979](https://github.com/csautter/dev-alchemy/commit/177c979171d01e1fc4086ef7b69da9e88141067f))
* **provision:** add UTM Windows 11 provisioning support ([556ce74](https://github.com/csautter/dev-alchemy/commit/556ce749c58af9f2998f9f3f53840e5e1f75c6a0))
* **provision:** add UTM Windows 11 provisioning support ([baacca3](https://github.com/csautter/dev-alchemy/commit/baacca35df2846beb061057978ba17bc3bd4304e))
* **provision:** allow overriding ansible playbook path ([71f74a3](https://github.com/csautter/dev-alchemy/commit/71f74a3b63d91d2b48d295e1ab31385bb1a399cc))
* **provision:** allow role source config to set playbook ([f92d1ef](https://github.com/csautter/dev-alchemy/commit/f92d1ef4dc2912d66627ec37040dd9bce8acf07d))
* **provision:** secure local Windows WinRM provisioning ([ba8f592](https://github.com/csautter/dev-alchemy/commit/ba8f5928050965d821d7d77c550e66d135883691))
* **provision:** support configurable ansible role sources ([5482f80](https://github.com/csautter/dev-alchemy/commit/5482f8003cdb65bd03d5aecabfb0743cfc4631e0))
* **provision:** support configurable playbook sources ([23ce7fc](https://github.com/csautter/dev-alchemy/commit/23ce7fc8e7f305aed5b707908b6882d92fd793d1))
* **provision:** support Ubuntu provisioning on Linux libvirt ([9f21d04](https://github.com/csautter/dev-alchemy/commit/9f21d04a9bab847af709ca17f5c13c30d146f89b))
* restructure docs ([585b871](https://github.com/csautter/dev-alchemy/commit/585b8713c75ab8d24cc79397810344a03acd22f8))
* **tart:** add macOS Tart target for create and provision ([e721799](https://github.com/csautter/dev-alchemy/commit/e7217997e0b7672d81347202d7462f454034b905))
* **tart:** add macOS Tart target for create and provision ([e0203c9](https://github.com/csautter/dev-alchemy/commit/e0203c9bb1624ebee751fc0502eb535ec9feef12))
* **vm:** add lifecycle commands for starting, stopping, and destroying managed VMs ([529adfa](https://github.com/csautter/dev-alchemy/commit/529adfa3ca28c7a45377cefc18217bfa5f1fa6fd))
* **vm:** add start command and fail-fast provision preflight ([1cb7a7a](https://github.com/csautter/dev-alchemy/commit/1cb7a7a593827bfe9b1deba4bab43b2a12bc2223))
* **vm:** add stop command with graceful UTM shutdown ([79b5689](https://github.com/csautter/dev-alchemy/commit/79b568916c4a5a0924d1519425fe2dfda2313bed))
* **windows:** unify runner dependency setup behind self-setup script ([e5aaa4a](https://github.com/csautter/dev-alchemy/commit/e5aaa4abc88796ebc1f33d3f499294cdf7bc4ccf))
* **windows:** unify runner dependency setup behind self-setup script ([8b151a5](https://github.com/csautter/dev-alchemy/commit/8b151a55c7e72e6f7fb8bb51b03172e21fd5378f))


### Fixed

* **actions:** simplify Hyper-V queue watcher job parsing ([4bbaff8](https://github.com/csautter/dev-alchemy/commit/4bbaff841f83280acac627448eb126f4132bd536))
* address multiple gosec findings ([b3a2cf1](https://github.com/csautter/dev-alchemy/commit/b3a2cf1582e2a5b5060f34cc81115b8744dde930))
* **ansible:** make installer roles check-mode safe ([f0b1f37](https://github.com/csautter/dev-alchemy/commit/f0b1f373a1e547723cbbd7daa0d83336d8d484c4))
* **ansible:** resolve macOS Java homes on target before jenv add ([a408ef2](https://github.com/csautter/dev-alchemy/commit/a408ef24ee458ceb8f85807d9d4c19d50aa45b18))
* **build:** add engine selection and mark virtualbox unstable ([8434021](https://github.com/csautter/dev-alchemy/commit/8434021c904b737eb38826c98b84dfbdea48a12a))
* **build:** clean failed artifacts and repair arm64 UEFI extraction ([7da46f8](https://github.com/csautter/dev-alchemy/commit/7da46f8c5372265889883a786339c7d9cd295c6f))
* **build:** clean up incomplete artifacts after failed builds ([34988e7](https://github.com/csautter/dev-alchemy/commit/34988e74d28f10ae09c79dbbf3ae0ffa7462860c))
* **build:** dependency download bar speed calculation ([1cd7da5](https://github.com/csautter/dev-alchemy/commit/1cd7da5ef213fc16904b697340457eaa5ddc56b7))
* **build:** ensure Windows 11 amd64 QEMU downloads virtio ISO ([a3b3dc5](https://github.com/csautter/dev-alchemy/commit/a3b3dc58a952e6438e27148abb3e712c1ec9c971))
* **build:** exclude unstable virtualbox target from build all by default ([0876478](https://github.com/csautter/dev-alchemy/commit/087647888cd14ce7d18115c6577bce492ce7185f))
* **build:** extend Linux QEMU build timeouts to five hours ([9ba19ce](https://github.com/csautter/dev-alchemy/commit/9ba19cec2b1cff213ae5743024cbc0687aa495a0))
* **build:** extract Windows ARM64 ISO with UDF-capable tools ([447ce84](https://github.com/csautter/dev-alchemy/commit/447ce841c527929ab83ada3ab20e986f808228d4))
* **build:** handle dependency downloads without progress bars ([d11a8e9](https://github.com/csautter/dev-alchemy/commit/d11a8e92e49958d83c32979afdceb3499a727493))
* **build:** harden embedded project extraction against gosec findings ([b850868](https://github.com/csautter/dev-alchemy/commit/b8508683e7233e3e2d5e246a6aa3048c43cf179f))
* **build:** harden unix tty fd handling for gosec compliance ([5cfa18c](https://github.com/csautter/dev-alchemy/commit/5cfa18c4215b82d0bdc968334c27e538f695e059))
* **build:** improve interrupt cleanup and preserve ffmpeg on graceful cancel ([57b254f](https://github.com/csautter/dev-alchemy/commit/57b254fe3a4b9c5751a516312c7bfe70b1f62fc4))
* **build:** install QXL driver during Windows QEMU builds ([799e130](https://github.com/csautter/dev-alchemy/commit/799e130add24e2e75083ae6d228608b64bc51800))
* **build:** keep Ubuntu ISO checksums in sync ([02a86e3](https://github.com/csautter/dev-alchemy/commit/02a86e32ac33e10ed9849ac714a587540e4a76c5))
* **build:** preserve cached artifacts during no-cache builds ([dff2459](https://github.com/csautter/dev-alchemy/commit/dff2459b4192713d62cefa02ab70e245fafafbd3))
* **build:** preserve successful artifacts after build cleanup ([26a1ec9](https://github.com/csautter/dev-alchemy/commit/26a1ec97fe11dca2815fcee232c8008e0e572e2b))
* **build:** preserve sudo prompts while restoring helper process logs ([82155e9](https://github.com/csautter/dev-alchemy/commit/82155e988c0f62f854a8d36922a00686d4629754))
* **build:** quote Ubuntu QCOW2 export paths in packer templates ([46fe595](https://github.com/csautter/dev-alchemy/commit/46fe5951877b9598518f7f017857f631b36ae801))
* **build:** removed de keymap from qemu args to avoid wrong inputs in boot_command ([00b924f](https://github.com/csautter/dev-alchemy/commit/00b924f262bc3797b52d71e5c566b204ce1b86ba))
* **build:** resolve Python executable for Windows ISO bootstrap ([eba7c09](https://github.com/csautter/dev-alchemy/commit/eba7c09bbd3740e0fa8f945b59c7ea115ac4e877))
* **build:** restrict managed app directory permissions to 0700 ([2bc4f7d](https://github.com/csautter/dev-alchemy/commit/2bc4f7de660dd7112ce6bf85a5ed759c916a8c65))
* **build:** retry virtio-win download from fallback sources ([37c70e9](https://github.com/csautter/dev-alchemy/commit/37c70e94132f5af6d96ae33da07f59b158b5f65f))
* **build:** shorten macOS Packer start-only temp paths ([40ec4d1](https://github.com/csautter/dev-alchemy/commit/40ec4d13645c8749d5ccb14fe5dfe8f51ec72e46))
* **build:** shorten macos qemu output paths and stop noisy vnc retries ([20354ec](https://github.com/csautter/dev-alchemy/commit/20354ec011cd5d5d394e091e622061f1814971d8))
* **build:** skip unchanged embedded project sync and prune stale assets ([c8ddc24](https://github.com/csautter/dev-alchemy/commit/c8ddc24f0a5d714cb7b94d15fdf715af97285559))
* **build:** stop external process retries immediately on interrupt ([24bf8da](https://github.com/csautter/dev-alchemy/commit/24bf8da8c02cfaa26d1c9b0cbbe54792d9c92296))
* **build:** tune QEMU CPU and memory settings for Ubuntu VMs ([bd3913c](https://github.com/csautter/dev-alchemy/commit/bd3913c3a8c620ef2a36d232988a829eb88e80fb))
* **build:** verify virtio-win ISO checksum ([d4acd37](https://github.com/csautter/dev-alchemy/commit/d4acd37c520233f4f5582727a12b842a2fa3ddaf))
* **build:** write Windows release zip to dist for artifact upload ([ca05356](https://github.com/csautter/dev-alchemy/commit/ca0535682c2dc7dc0c8feebe881a0773c8f58b96))
* **cache:** use portable symlink resolution for uploads ([b5ae57a](https://github.com/csautter/dev-alchemy/commit/b5ae57a1acee7db05d992c7b9cc976a8f8b979fa))
* **ci:** accept Homebrew formula revisions ([56a94f5](https://github.com/csautter/dev-alchemy/commit/56a94f522fab8a565d5435d88b57172c437979a5))
* **ci:** accept newer Homebrew dependency versions ([cf41138](https://github.com/csautter/dev-alchemy/commit/cf411380b8e679affd002787761b364e7c01d2b2))
* **ci:** align macOS workflow cache paths with managed app data ([0e08270](https://github.com/csautter/dev-alchemy/commit/0e082708048154c32f3b4e0e91de756805f04aba))
* **ci:** align Windows workflow cache paths with managed app data ([c0e419a](https://github.com/csautter/dev-alchemy/commit/c0e419a43d7d0de214d07fc36733ac415a89d8a0))
* **ci:** capture sanitized Hyper-V diagnostics for Windows build failures ([bfb7246](https://github.com/csautter/dev-alchemy/commit/bfb72463ccb442f26980fb00b1c3996d45395571))
* **ci:** harden Playwright Windows ISO fetch and upload diagnostics ([99b0cf3](https://github.com/csautter/dev-alchemy/commit/99b0cf390348d4d4c09ee48165273a0e0c1ba099))
* **ci:** keep Ubuntu ISO cache references in sync ([80444ec](https://github.com/csautter/dev-alchemy/commit/80444ec3fa223ee17b118e56f19d96faa18f48f9))
* **ci:** pass repository to self-hosted renovate action ([7e60475](https://github.com/csautter/dev-alchemy/commit/7e604754a64fdf08c27f52d0c6287f1cc54e3445))
* **ci:** restore artifact directory access after gosec hardening ([2855f00](https://github.com/csautter/dev-alchemy/commit/2855f000245dcd7dd1c885b07d831b093f3bec48))
* **ci:** restore cache ownership after sudo macos builds ([fc9e4e2](https://github.com/csautter/dev-alchemy/commit/fc9e4e2edbb7b5445c9ada621e9e9a77931b26e4))
* **ci:** restrict GHCR publish permissions ([2a6f990](https://github.com/csautter/dev-alchemy/commit/2a6f9900976516c44886cc6f0f97ffeb4c0f3cfe))
* **ci:** retry HyperV runner cleanup before canceling queued builds ([a4a710c](https://github.com/csautter/dev-alchemy/commit/a4a710cfb89f85d58fd5dab275851d1d41187e35))
* **ci:** run Windows winrm role test via provision local ([3585a69](https://github.com/csautter/dev-alchemy/commit/3585a69eb5593ba944b9bcc617222873336261b1))
* **ci:** show progress during FTPS build cache downloads ([80ae228](https://github.com/csautter/dev-alchemy/commit/80ae228d85a9957f54b74bbb4d9190c398504207))
* **ci:** specify repository for gh release upload ([f693ae1](https://github.com/csautter/dev-alchemy/commit/f693ae1277228050c7df9f9f64d9dca98f7addc7))
* **ci:** trigger PR workflows on cmd/main.go changes ([459c297](https://github.com/csautter/dev-alchemy/commit/459c2971d6da37e1e37e5fa0c0a844426ea52a0e))
* **cli:** mark cross-arch VM targets unstable ([4ee93ea](https://github.com/csautter/dev-alchemy/commit/4ee93eaad85ec55f04f2ddd73ec9ff874941cf22))
* **cli:** remove duplicated root help sections ([22fd4f1](https://github.com/csautter/dev-alchemy/commit/22fd4f1ccaaaaaeb7e698a744644523c6ab7b006))
* **deploy:** address gosec findings in tart deploy helpers ([b564d3f](https://github.com/csautter/dev-alchemy/commit/b564d3f28cb6c90bc658b6c199dfd7e6f3380d6c))
* **deploy:** configure QXL video memory for Windows 11 libvirt guests ([d60a591](https://github.com/csautter/dev-alchemy/commit/d60a5917711275a4c2a727e0f4ccc01db7e21b1c))
* **deploy:** fail fast when Cygwin bash is missing on Windows provisioning ([c34f0cc](https://github.com/csautter/dev-alchemy/commit/c34f0cc74b15c35b1d1574dba2189f663e10fdd1))
* **deploy:** handle existing running Tart macOS VMs explicitly ([6ee6e8c](https://github.com/csautter/dev-alchemy/commit/6ee6e8ce06dc4edd1157a61fb4567277ff1491dc))
* **deploy:** harden Hyper-V Vagrant stop with forced halt fallback ([fb75cdb](https://github.com/csautter/dev-alchemy/commit/fb75cdbffacf72353ec50cf56d5c94764ba2bfdb))
* **deploy:** harden libvirt image directory access ([8aa0033](https://github.com/csautter/dev-alchemy/commit/8aa00330b875338d57e8e5368e520ee1df1a7a3c))
* **deploy:** infer canonical UTM targets for macOS deploy configs ([893d616](https://github.com/csautter/dev-alchemy/commit/893d616cace68f17fcdff80145f5c30dad94f11a))
* **deploy:** inspect Hyper-V VM state via PowerShell ([21bd7da](https://github.com/csautter/dev-alchemy/commit/21bd7da2d964006ce4b6c2af87befcc969393f7e))
* **deploy:** keep libvirt ACL hints POSIX on Windows ([075404c](https://github.com/csautter/dev-alchemy/commit/075404c6e48567c0256e16d986db8f5fe7dca7c2))
* **deploy:** make emulated arm64 libvirt domains portable ([cb34c74](https://github.com/csautter/dev-alchemy/commit/cb34c747d07c5d6e78b51692784d4faeff95e4ee))
* **deploy:** make Hyper-V stop timeout boundary deterministic ([36f9499](https://github.com/csautter/dev-alchemy/commit/36f9499547f7e20eefc0dad6cbea616a33f21ffb))
* **deploy:** normalize Hyper-V Vagrant dotfile env paths on Windows ([0aec0d9](https://github.com/csautter/dev-alchemy/commit/0aec0d9f69dc696c0475e110f83223f511b6d5ea))
* **deploy:** parse tart list output by columns for local VM lookup ([715b704](https://github.com/csautter/dev-alchemy/commit/715b70498ed060c91442d635111f23deda6929a9))
* **deploy:** preflight libvirt named networks ([f886bf7](https://github.com/csautter/dev-alchemy/commit/f886bf7628b23b68144ba13087fe622a7007df61))
* **deploy:** prevent truncated streamed logs and surface scanner errors ([7724f4a](https://github.com/csautter/dev-alchemy/commit/7724f4a91bad9cda12bd89883fec822077a52f3e))
* **deploy:** re-prime UTM ARP cache while waiting for guest IPv4 ([47206e9](https://github.com/csautter/dev-alchemy/commit/47206e9e71426e12e7c7f66b082089997d142b55))
* **deploy:** remove windows build constraints from hyperv deploy file ([5c86ebe](https://github.com/csautter/dev-alchemy/commit/5c86ebe52e85f7bbf5dce9cca867a8c4ca07e04f))
* **deploy:** retry UTM IP discovery and warm ARP cache ([1f40162](https://github.com/csautter/dev-alchemy/commit/1f40162297960058b3c74a269990a32aa87a7e79))
* **deploy:** return command execution errors instead of panicking ([9d881bf](https://github.com/csautter/dev-alchemy/commit/9d881bfc6344015ce512068e780d07ca9fbaae9e))
* **deps:** upgrade google.golang.org/grpc to v1.79.3 for GO-2026-4762 ([9942bd7](https://github.com/csautter/dev-alchemy/commit/9942bd7327db700b97c6da12b078f08bd8a3e02f))
* **devcontainer:** verify PowerShell tarball checksum ([a5deb63](https://github.com/csautter/dev-alchemy/commit/a5deb63e5141f815280eed655b7a9739d45b74ee))
* **gha:** restore native go and python in Windows Azure runner images ([1a08df0](https://github.com/csautter/dev-alchemy/commit/1a08df021872e746b5a3bcd52ed8bd85d527009a))
* **github-action:** enable IPv6 and disable IPv4 by default for Hetzner runner ([de96703](https://github.com/csautter/dev-alchemy/commit/de967031609d2ab422c2b47093c275ad803286da))
* handle empty cookies.json by catching JSONDecodeError ([477fe1d](https://github.com/csautter/dev-alchemy/commit/477fe1dce64b5bbf1ab248a1838b0e304b6e744e))
* **hyperv:** isolate vagrant state per vm and prevent false create detection ([eabb917](https://github.com/csautter/dev-alchemy/commit/eabb9172a7f78034b64b8b886c7e8890c079c6ca))
* **kubectl:** avoid Windows Chocolatey failure when newer kubernetes-cli is already installed ([174b1fd](https://github.com/csautter/dev-alchemy/commit/174b1fdce174a12e89050f9758e8d42cf4055da8))
* **kubectl:** clean up apt key temp dir on setup failure ([1a58b5e](https://github.com/csautter/dev-alchemy/commit/1a58b5edc056958588a13572fb1673d5b8dd5a50))
* **kubectl:** make Debian apt key setup resilient ([1efa8cc](https://github.com/csautter/dev-alchemy/commit/1efa8cc04a7b3608a2c5a77cc77e4b4fcd024a10))
* **kubelogin:** authenticate GitHub release lookup in CI ([e2cd924](https://github.com/csautter/dev-alchemy/commit/e2cd9242cb08478a37e7777aca45aaf6c74fed85))
* **libvirt:** install QXL driver for Windows auto-resize ([271a76d](https://github.com/csautter/dev-alchemy/commit/271a76deb713f53652250dfb6293dce9df33b51d))
* **linux:** stabilize Ubuntu desktop QEMU/libvirt networking and desktop image setup ([f3de076](https://github.com/csautter/dev-alchemy/commit/f3de076478c15928dad55c86571b17728cb6f89b))
* listener bar null check ([25a688a](https://github.com/csautter/dev-alchemy/commit/25a688ab32b5949e05bc0b83445d802f68de3a95))
* **macos:** detect latest installed Homebrew version ([1ab1fff](https://github.com/csautter/dev-alchemy/commit/1ab1fffa732e18cce13ed27855400fcd2e8c826c))
* **macos:** pin Homebrew tooling installs ([de21704](https://github.com/csautter/dev-alchemy/commit/de217045981b82f0f62d6844425a8a197a624c33))
* **macos:** warn on unavailable pinned brew versions ([9701275](https://github.com/csautter/dev-alchemy/commit/9701275093a024e0241ed78b71e2061a4f8f841e))
* make vncsnapshot retry loop interruptible by SIGINT/SIGTERM ([a18eb8d](https://github.com/csautter/dev-alchemy/commit/a18eb8df7aaf750b9d175243c5d428f315b6c1cf))
* **make:** remove brittle pkill cleanup after build tests ([9c5c0da](https://github.com/csautter/dev-alchemy/commit/9c5c0dab03dc5268332b411ddb7adeddbca328e7))
* **oci:** reject duplicate manifest layer titles ([8f389b9](https://github.com/csautter/dev-alchemy/commit/8f389b923977dd121a9b0eb76dbe6d51ebad1685))
* **oci:** suppress gosec G402 on insecure TLS field ([c5d970d](https://github.com/csautter/dev-alchemy/commit/c5d970d6a383870b00bc3ee895252425180392da))
* **packer:** move qemu ubuntu package installs out of cloud-init ([66ee86b](https://github.com/csautter/dev-alchemy/commit/66ee86ba471a119dd1839d189d423841c6a1c116))
* **packer:** persist ARM64 Windows EFI boot state ([b56c04a](https://github.com/csautter/dev-alchemy/commit/b56c04a58184d6be169c3bde50b57e86f2288505))
* **packer:** refresh PATH after Windows setup provisioning ([1144bc7](https://github.com/csautter/dev-alchemy/commit/1144bc7b188b05e14f09af8586b3434d918fd2fa))
* **packer:** restore ARM64 Windows CI boot order ([708ca62](https://github.com/csautter/dev-alchemy/commit/708ca62c8b69a20bb52c8a3727a29e3cd49df096))
* **packer:** restore persistent DHCP config for QEMU Ubuntu images ([072a676](https://github.com/csautter/dev-alchemy/commit/072a6761a3ff6a93794a83c0483d3865aa6167e8))
* **packer:** simplify QEMU autoinstall networking ([261d1da](https://github.com/csautter/dev-alchemy/commit/261d1da193298616639ce7fb32b83740ec5e68bf))
* **packer:** simplify ubuntu desktop qemu provisioning ([cfbd97c](https://github.com/csautter/dev-alchemy/commit/cfbd97c7fa0e360ed00aaf21d5335d6ed44cbbcb))
* **packer:** stabilize ARM64 Ubuntu QEMU installs ([f592a10](https://github.com/csautter/dev-alchemy/commit/f592a10f304595bb80682b289da42f9e9b3e6ea1))
* **packer:** use valid validation messages for cache_dir ([aa924f4](https://github.com/csautter/dev-alchemy/commit/aa924f426b3f23c29ec46ef31797c53e9a062dde))
* prevent deadlock in stopVncScreenCaptureOnMacosDarwin when vncsnapshot exits cleanly ([784183b](https://github.com/csautter/dev-alchemy/commit/784183bd0968f11f8d67ac428968f1183a5382eb))
* prevent deadlock in stopVncScreenCaptureOnMacosDarwin when vncsnapshot exits cleanly ([9c2ca4f](https://github.com/csautter/dev-alchemy/commit/9c2ca4faecf2f783bd28e56ca86c3f9d54e557ff))
* **provision:** avoid reboot-only local Windows SSH cleanup and log OpenSSH install progress ([b480986](https://github.com/csautter/dev-alchemy/commit/b48098658d124ebde491759fe9b29685f9205b27))
* **provision:** bind local Windows WinRM bootstrap to loopback ([768eeed](https://github.com/csautter/dev-alchemy/commit/768eeedd337f13610d8f6bb8d5bd450d54fb2b3a))
* **provision:** detect Hyper-V Ubuntu guest IP without key-based vagrant ssh ([0cba4c3](https://github.com/csautter/dev-alchemy/commit/0cba4c3c50713484ec9d4ba96083cf64073d4e8c))
* **provision:** elevate and confirm local Windows WinRM bootstrap ([dc0adaa](https://github.com/csautter/dev-alchemy/commit/dc0adaa77dce00cc13ff4b3db99824af964fb5ea))
* **provision:** enforce documented ansible verbosity range ([5d1dd8a](https://github.com/csautter/dev-alchemy/commit/5d1dd8a6a3b19fa49c052d5839645d2ba165c723))
* **provision:** guard SSH wire length encoding against uint32 overflow ([ec9ef74](https://github.com/csautter/dev-alchemy/commit/ec9ef749d673c324177032d7685004ebdcb821e0))
* **provision:** harden local Windows SSH bootstrap state handling ([e2e187e](https://github.com/csautter/dev-alchemy/commit/e2e187e6996b7f79e3ad5112c399e1fa00829cf1))
* **provision:** harden local Windows SSH port selection and cleanup ([725b827](https://github.com/csautter/dev-alchemy/commit/725b827114748b41e04a675e44b172115a1a5f56))
* **provision:** harden local Windows WinRM bootstrap cleanup ([310d4f1](https://github.com/csautter/dev-alchemy/commit/310d4f123bdb5e3be663edc7602fadeeae4d0c92))
* **provision:** harden role source runtime handling ([027aa53](https://github.com/csautter/dev-alchemy/commit/027aa53e60c461ac6ac3f6bbb34bd13e2deb21d1))
* **provision:** ignore IPv6 hostnames in linux vagrant IP discovery ([836fc02](https://github.com/csautter/dev-alchemy/commit/836fc02f84c8ac95e00decd9705e2caa5be23b98))
* **provision:** improve local Windows SSH auth setup, cleanup, and preflight diagnostics ([f2572a2](https://github.com/csautter/dev-alchemy/commit/f2572a2c690b1b6bace3391ba5340fd01ea83c08))
* **provisioning:** install ansible in devcontainer and fix kubelogin header templating ([4786109](https://github.com/csautter/dev-alchemy/commit/47861098309d6ea02f7e9690e8863b0a37592ea0))
* **provision:** log local Windows PowerShell bootstrap and cleanup progress ([146a8f1](https://github.com/csautter/dev-alchemy/commit/146a8f1210d1c0596dea72e3981b67b6981576ba))
* **provision:** preflight sshpass for password SSH auth ([0409721](https://github.com/csautter/dev-alchemy/commit/0409721fef13ceec2804b46b3827e0ae2ecd1b62))
* **provision:** skip Windows RunAs hop in elevated shells ([42ce281](https://github.com/csautter/dev-alchemy/commit/42ce2815c8d0f65417a61f7f231696040484a0b0))
* **provision:** use a dedicated local Windows SSH port and detect wrong SSH listeners ([db65e64](https://github.com/csautter/dev-alchemy/commit/db65e64f579ece5f6d94393721b967746517cefd))
* **provision:** use HTTPS-only local WinRM bootstrap on Windows ([a1fe0e7](https://github.com/csautter/dev-alchemy/commit/a1fe0e7e4644336a132068c61210dc4c44e1a8fe))
* **provision:** use VM-specific Hyper-V Vagrant settings ([5c78d8d](https://github.com/csautter/dev-alchemy/commit/5c78d8dd7a425459dd70c863ae82151d4a6ecb40))
* **qemu:** tune cross-arch emulation CPU settings ([618b13c](https://github.com/csautter/dev-alchemy/commit/618b13c53c761faa6498f035a2dc0b2b0ad3943b))
* **renovate:** resolve packer plugin lookups and set git author ([626671c](https://github.com/csautter/dev-alchemy/commit/626671c4e12bd525f45b4a27240fd1cf211c98ad))
* **runners:** cancel active macos tart jobs before shutdown ([fd8f296](https://github.com/csautter/dev-alchemy/commit/fd8f296ca6e0303a8c0d56c529cee2cc4225f67f))
* **runners:** cancel active macos tart jobs before shutdown ([1466ec9](https://github.com/csautter/dev-alchemy/commit/1466ec96b7745d2a9b7a82bb47aeb11f5fb24aab))
* **runners:** normalize shutdown poll interval ([a8a98c8](https://github.com/csautter/dev-alchemy/commit/a8a98c8afe2283255bba79492d65ac63cc408de0))
* **runners:** preserve busy macos tart VMs during shutdown ([089be7b](https://github.com/csautter/dev-alchemy/commit/089be7b19f673288e3fe36fd35a18e5101caefd5))
* **runners:** preserve unknown GitHub API state during cleanup ([75d9fa7](https://github.com/csautter/dev-alchemy/commit/75d9fa763721ac784dee3d8d998d6898e50130de))
* **runners:** report force-cancel failures during shutdown ([81a131c](https://github.com/csautter/dev-alchemy/commit/81a131c23ca308bf088697901c521b19e843f49f))
* **runners:** wait for tart runner deregistration on shutdown ([8c94361](https://github.com/csautter/dev-alchemy/commit/8c9436176fb4bd32bf26b0b03b70ef046965513e))
* **security:** harden VNC recording file and process handling ([069461e](https://github.com/csautter/dev-alchemy/commit/069461e57e08bfa3261c7cb1fb86a3dd650d99ee))
* **security:** triage and suppress provision gosec findings ([a528c99](https://github.com/csautter/dev-alchemy/commit/a528c99c09de1ecece4b1fe5372488c1b2d34f98))
* stop re-rendering completed progress bars ([d5d4ee8](https://github.com/csautter/dev-alchemy/commit/d5d4ee8355d00c5e63471be3b77ae377782de7bf))
* **stop:** use Hyper-V graceful shutdown for Ubuntu VMs before force halt ([89f99b9](https://github.com/csautter/dev-alchemy/commit/89f99b93f41d8d6e63ea8fab67d14018fb987784))
* **test:** normalize workflow line endings for Windows CI ([43f77a0](https://github.com/csautter/dev-alchemy/commit/43f77a0bfccf7f80cb90f8a44ebf0a0a3f918c6a))
* update downloadWebFileDependency call in test to pass progress argument ([c6a8cfc](https://github.com/csautter/dev-alchemy/commit/c6a8cfccc8d8b94c9e8277a738db05d17f901974))
* **utm:** set arm64 cpu type to default to fix issues with vm boot ([0773b0f](https://github.com/csautter/dev-alchemy/commit/0773b0f61a828e7ddb191abba60ad9028ed5c380))
* **windows11:** fix arm64 QEMU boot hang and headless mode handling ([a9bb0ad](https://github.com/csautter/dev-alchemy/commit/a9bb0ad3f250a451515f4e31d864d8cbced622c9))
* **windows:** fall back to alternate cygwin install root ([4de65ac](https://github.com/csautter/dev-alchemy/commit/4de65acd643269af3e203bbd189e6ed527223475))
* **windows:** install the correct Chocolatey Python package ([fde8260](https://github.com/csautter/dev-alchemy/commit/fde826047232f529ca0c4b4073c3bbf9500b43ca))
* **windows:** keep WinRM reachable across network profile changes ([00d0dcf](https://github.com/csautter/dev-alchemy/commit/00d0dcfdcdad107945566e8d6321a6edd22d7465))
* **windows:** make Go setup opt-in for dependency installs ([ccdb8f2](https://github.com/csautter/dev-alchemy/commit/ccdb8f20f03f6a4345a5ee0ef7f1221eb419a903))
* **windows:** preserve existing ssh provision user on force uninstall ([e7a643c](https://github.com/csautter/dev-alchemy/commit/e7a643ccfa3270ba93d61a605fec19bfd6640154))
* **windows:** reinstall cygwin cleanly on version mismatch ([1f13700](https://github.com/csautter/dev-alchemy/commit/1f137002e4e50a388ba536acff9e642112f331c4))
* **windows:** validate Cygwin root before cleanup ([5b7c61c](https://github.com/csautter/dev-alchemy/commit/5b7c61ce6fddc66a239904ff85eaef51cf2ee58a))
* **windows:** verify selected cygwin root before package setup ([6f01d82](https://github.com/csautter/dev-alchemy/commit/6f01d82309e01ecde5f01e00af5965065b403bd1))


### Changed

* **build:** extract VNC recording logic into dedicated file ([49dadec](https://github.com/csautter/dev-alchemy/commit/49dadec069aacd9354e9f4e4f989dcf56a9d4de3))
* **build:** removed windows isos az storage container ([5b7843b](https://github.com/csautter/dev-alchemy/commit/5b7843bd1b2473606a18e9e01dd57aa3106cee93))
* deduplicate build/create/provision VM list rendering ([75af05e](https://github.com/csautter/dev-alchemy/commit/75af05e8f9c6947018967bf44da1941f2e00a2b8))
* **deploy:** extract shared SSH wait helper ([35d32cd](https://github.com/csautter/dev-alchemy/commit/35d32cd503f4733f8692bc88c09b2fccde43f54b))
* **deploy:** replace static winrm inventory with env-var-driven ansible config ([c87bf2b](https://github.com/csautter/dev-alchemy/commit/c87bf2bf388ddb2eb814a7b063f3c57a93bcf372))
* move VM state into managed app data ([9697909](https://github.com/csautter/dev-alchemy/commit/9697909783a3b774a782a3706e17dfb52261b437))
* move VM state into managed app data ([1077820](https://github.com/csautter/dev-alchemy/commit/1077820edb0fcdb1e7e72f5a57134c13420bf4b9))
* **oci:** split artifact handling into focused modules ([850ef4f](https://github.com/csautter/dev-alchemy/commit/850ef4f9dd9df3503f627fae805b4481d74bff1c))
* **packer:** split linux and macos ubuntu qemu build paths ([6dbfc44](https://github.com/csautter/dev-alchemy/commit/6dbfc442b6a5d25bef6162782ab4fae094869c03))
* **packer:** unify Ubuntu QEMU templates ([ff189e1](https://github.com/csautter/dev-alchemy/commit/ff189e1e6526cd84873f6717799848cf625b12a6))
* **provision:** deduplicate local windows session runner flow ([786e286](https://github.com/csautter/dev-alchemy/commit/786e286a36f5e71793e4fde2f7aca2980f6a8134))
* **provision:** extract local Windows WinRM scripts into ps1 assets ([60971c8](https://github.com/csautter/dev-alchemy/commit/60971c80b58a5e5c8dcdf999f4ec56fab1dd6966))
* **provision:** move VM provisioning logic out of pkg/deploy ([f6cc593](https://github.com/csautter/dev-alchemy/commit/f6cc5937910ba70932123f90a4b88a30989bb37e))
* **provision:** move windows local logic into dedicated file ([80c6895](https://github.com/csautter/dev-alchemy/commit/80c68954144ab64974b680a7fabbe969b790d207))
* **provision:** reject "all" target with explicit error ([d94c306](https://github.com/csautter/dev-alchemy/commit/d94c306270fd166f3c8f9eb512a0c78b51c7b537))
* **provision:** share managed Windows user lifecycle across ssh and winrm ([3a685d7](https://github.com/csautter/dev-alchemy/commit/3a685d704458aa1fae89964629eeac4ae0a161e1))
* rename shared SSH provision arg builder ([f1b0629](https://github.com/csautter/dev-alchemy/commit/f1b0629a6949361a3034d6e255e33c30e2aef729))
* resolve VirtualMachineConfig from available list instead of constructing manually ([f9730b4](https://github.com/csautter/dev-alchemy/commit/f9730b432b2cbb0dd14c46d0caed64ffc8c1b599))


### CI

* **actions:** add workflow concurrency to prevent push/pr parallel runs ([f5498b1](https://github.com/csautter/dev-alchemy/commit/f5498b13a9b2c4a7be3c0972efcd3fae2383589b))
* **actions:** add workflow concurrency to prevent push/pr parallel runs ([b875e69](https://github.com/csautter/dev-alchemy/commit/b875e690efa3d7e1591c5bb10ddff2efcd2c75a1))
* add cold-cache dependency bootstrap coverage to Linux workflow ([9511385](https://github.com/csautter/dev-alchemy/commit/9511385ffd5d66b18e6daec76c90ed31ccab164f))
* add deploy smoke tests to macOS and Windows build workflows ([93951cf](https://github.com/csautter/dev-alchemy/commit/93951cfbe62fd6bc20fbb0271c76f5f677419749))
* add Go security scanning workflow (govulncheck + gosec) ([c786bd5](https://github.com/csautter/dev-alchemy/commit/c786bd55110db26b5599102bb65e45af6d172939))
* add Makefile target and GitHub Actions workflow for build-runner tests ([4d7d305](https://github.com/csautter/dev-alchemy/commit/4d7d305b510f72a81ef09504318dadcdca24b622))
* add native Linux QEMU smoke builds for amd64 and arm64 ([fe056bb](https://github.com/csautter/dev-alchemy/commit/fe056bb3400dd96230c1d6b30b65751a9cae41eb))
* add PR coverage for deploy and provision tests ([33dd2a3](https://github.com/csautter/dev-alchemy/commit/33dd2a316600ad176c3501ff605e382fc638549d))
* add shellcheck workflow ([05eb93c](https://github.com/csautter/dev-alchemy/commit/05eb93cdf377e5e2ea96f7e4df3ba4b940736075))
* align Windows and macOS build job timeouts with packer timeouts ([2d72d46](https://github.com/csautter/dev-alchemy/commit/2d72d4659e8523c02e6ed48fdbb42dd44ecf5f2e))
* broaden build workflow path filters for shared build inputs ([f6bf616](https://github.com/csautter/dev-alchemy/commit/f6bf616df1c6c4a2805182cc095b17b014cd2b0f))
* cancel Windows Hyper-V test runs when the runner stays queued ([30c9852](https://github.com/csautter/dev-alchemy/commit/30c98526f07c44d4a87a20a58b2c140591c6191c))
* configure release-please for automated releases ([95e3fd7](https://github.com/csautter/dev-alchemy/commit/95e3fd762e6f811c5ad7b918ffeed31daa0b6d1d))
* default macOS build tests to GitHub-hosted runners ([23adc30](https://github.com/csautter/dev-alchemy/commit/23adc30e225c01186b7ae3bce3cc6c2863bf1f83))
* **deps:** schedule Renovate in GitHub Actions ([86f5734](https://github.com/csautter/dev-alchemy/commit/86f5734a7d27d81cfc5a6d98801a1d6ae70e096f))
* exercise Windows local provision flows in ansible role tests ([95588e9](https://github.com/csautter/dev-alchemy/commit/95588e99e172e69be7dd41621da25237fef146a1))
* extend cmd unit-test workflow to cover provision and deploy tests ([95a4e50](https://github.com/csautter/dev-alchemy/commit/95a4e50be12377eb164e914ff54fea3cbc9cd8aa))
* fix unit-test workflow path filters to self-reference current file ([0849419](https://github.com/csautter/dev-alchemy/commit/084941958fb36d505ab278cdea9d41e021c576b8))
* include role source fixtures in test filters ([27891c6](https://github.com/csautter/dev-alchemy/commit/27891c60a1a279bf7c455a850b5b6ff9d24b82b0))
* publish ubuntu build artifacts to GHCR ([b0ed47d](https://github.com/csautter/dev-alchemy/commit/b0ed47d81af7a6482b5ba6b4fa1fceb41f00d388))
* refresh Azure auth before HyperV queue cleanup ([9189f40](https://github.com/csautter/dev-alchemy/commit/9189f400c91c24a464e8c0c8ef6cd76823f8c00a))
* remove Windows WinRM setup from ansible role test workflow ([0aef486](https://github.com/csautter/dev-alchemy/commit/0aef486c28d34b42bebc1d1fbdf21cebd04ff566))
* **renovate:** use branch config for workflow-dispatch runs ([11e6af7](https://github.com/csautter/dev-alchemy/commit/11e6af717e837269f21a06c1357e010472fd3993))
* restrict GHCR artifact publishing to main ([c74a1e2](https://github.com/csautter/dev-alchemy/commit/c74a1e2ccc53930abb51b59bde06accedd8a19d9))
* reuse install scripts in build workflows and make oscdimg setup idempotent ([6c9f033](https://github.com/csautter/dev-alchemy/commit/6c9f033c7f75361161aef84c4bd8e7f08e341200))
* reuse install scripts in build workflows and make oscdimg setup idempotent ([b10300c](https://github.com/csautter/dev-alchemy/commit/b10300cf302d0f251ad60dfe8844dc42b5b74bb1))
* run cmd and pkg/build unit tests in GitHub Actions ([630b74e](https://github.com/csautter/dev-alchemy/commit/630b74e11932df822c755efa5200f47af25c1181))
* run deploy and provision unit tests on main pushes ([23dc24f](https://github.com/csautter/dev-alchemy/commit/23dc24ffff8f76e7da0ccde7d6dad99d063181c9))
* run full cmd package tests in deploy unit workflow ([c8f1880](https://github.com/csautter/dev-alchemy/commit/c8f18804a4e3344ed7cd62822556ea866046a6e8))
* run gitleaks and cmd unit tests on main pushes ([4db3625](https://github.com/csautter/dev-alchemy/commit/4db3625756827014e0a124f8b4594b6c8c1d1f6c))
* run Linux Windows QEMU builds as start-only probes ([83acc0e](https://github.com/csautter/dev-alchemy/commit/83acc0efc276d02ca31114e2335e88576290e563))
* run OCI package unit tests ([ca1490f](https://github.com/csautter/dev-alchemy/commit/ca1490f4ff8404f7eae7b9838179e3b45ee12372))
* run OCI registry integration test ([23e8781](https://github.com/csautter/dev-alchemy/commit/23e87812471eb6e5fcaa8ad5d80c6383daa88c78))
* run release binary build dry-runs in pull requests ([4a0a132](https://github.com/csautter/dev-alchemy/commit/4a0a1320dd3291a3ab5e9da8af0abdb4f6be9884))
* run Windows 11 QEMU builds in Linux workflow ([ef832e1](https://github.com/csautter/dev-alchemy/commit/ef832e13817d747a0ac4d44d25d84fc7b2f6cb8e))
* scope Windows workflow actions write permission to queue watcher job ([4d3c23e](https://github.com/csautter/dev-alchemy/commit/4d3c23e20c1aa849e19fb82360cb6ea1df1c60a0))
* **security-scan:** don't checkout the code a second time ([b65153b](https://github.com/csautter/dev-alchemy/commit/b65153b4c7a793e846277491c5327ce6a85a9849))
* **security:** prevent gosec from failing workflow and gitignore sarif results ([e8e24e1](https://github.com/csautter/dev-alchemy/commit/e8e24e10491307f3f62c6d50fe8b2f94468a8738))
* set up Go before macOS build dependencies ([d02e9fe](https://github.com/csautter/dev-alchemy/commit/d02e9fe74230f99bd509a6c60ecbbd728bf0b8b4))
* shorten macOS Ubuntu packer build checks ([ad680c0](https://github.com/csautter/dev-alchemy/commit/ad680c09aafba667f469c0e904836a306eede1c7))
* store Hyper-V diagnostics in private Azure cach ([9e4d904](https://github.com/csautter/dev-alchemy/commit/9e4d9045c1fab0d301f12b8e1598e01d47d42206))
* trigger linux build on deploy runtime changes ([876cd00](https://github.com/csautter/dev-alchemy/commit/876cd00f33e25e0a199a87d495e12c1bc9505ca4))
* trigger macOS VM workflow for provision code changes ([907cc21](https://github.com/csautter/dev-alchemy/commit/907cc21a9047316af0dbbeb987871dbb5e61a924))
* validate build cache actions on changes ([b6849cf](https://github.com/csautter/dev-alchemy/commit/b6849cff42556be674dfc85250667a77596695eb))
* **windows:** set test-hyperv-build job timeout to 60 minutes ([d1086c3](https://github.com/csautter/dev-alchemy/commit/d1086c3709e606b0c0de28739cd6011520010e89))


### Miscellaneous

* release 0.16.1 ([0589ff0](https://github.com/csautter/dev-alchemy/commit/0589ff09c8cfde8df7f9e7dc7bc5f128e00f7b02))
* release 0.2.0 ([032ec53](https://github.com/csautter/dev-alchemy/commit/032ec53c14b2b687ef1cd4e6e70e871be14d7e88))

## [0.16.0](https://github.com/csautter/dev-alchemy/compare/v0.15.0...v0.16.0) (2026-05-17)


### Added

* ansible role source configurable ([79c6cd7](https://github.com/csautter/dev-alchemy/commit/79c6cd7c7a2369432605afb62fcdacd8a14c1157))
* **ci:** add FTPS build cache backend ([2d0884b](https://github.com/csautter/dev-alchemy/commit/2d0884b804e32c345826b371a08d6d5fee333f33))
* **ci:** add Hetzner S3 build cache backend ([d13c57b](https://github.com/csautter/dev-alchemy/commit/d13c57b2dc17b508092fae5b6df10432c969c02f))
* gh actions cache - add hetzner s3 and ftp ([7080cc0](https://github.com/csautter/dev-alchemy/commit/7080cc03f6a73680d57bc2f398c4e6bb4608e578))
* **provision:** allow role source config to set playbook ([f92d1ef](https://github.com/csautter/dev-alchemy/commit/f92d1ef4dc2912d66627ec37040dd9bce8acf07d))
* **provision:** support configurable ansible role sources ([5482f80](https://github.com/csautter/dev-alchemy/commit/5482f8003cdb65bd03d5aecabfb0743cfc4631e0))
* **provision:** support configurable playbook sources ([23ce7fc](https://github.com/csautter/dev-alchemy/commit/23ce7fc8e7f305aed5b707908b6882d92fd793d1))


### Fixed

* **build:** resolve Python executable for Windows ISO bootstrap ([eba7c09](https://github.com/csautter/dev-alchemy/commit/eba7c09bbd3740e0fa8f945b59c7ea115ac4e877))
* **cache:** use portable symlink resolution for uploads ([b5ae57a](https://github.com/csautter/dev-alchemy/commit/b5ae57a1acee7db05d992c7b9cc976a8f8b979fa))
* **ci:** show progress during FTPS build cache downloads ([80ae228](https://github.com/csautter/dev-alchemy/commit/80ae228d85a9957f54b74bbb4d9190c398504207))
* **devcontainer:** verify PowerShell tarball checksum ([a5deb63](https://github.com/csautter/dev-alchemy/commit/a5deb63e5141f815280eed655b7a9739d45b74ee))
* **packer:** persist ARM64 Windows EFI boot state ([b56c04a](https://github.com/csautter/dev-alchemy/commit/b56c04a58184d6be169c3bde50b57e86f2288505))
* **packer:** refresh PATH after Windows setup provisioning ([1144bc7](https://github.com/csautter/dev-alchemy/commit/1144bc7b188b05e14f09af8586b3434d918fd2fa))
* **packer:** restore ARM64 Windows CI boot order ([708ca62](https://github.com/csautter/dev-alchemy/commit/708ca62c8b69a20bb52c8a3727a29e3cd49df096))
* **provision:** harden role source runtime handling ([027aa53](https://github.com/csautter/dev-alchemy/commit/027aa53e60c461ac6ac3f6bbb34bd13e2deb21d1))
* **runners:** cancel active macos tart jobs before shutdown ([fd8f296](https://github.com/csautter/dev-alchemy/commit/fd8f296ca6e0303a8c0d56c529cee2cc4225f67f))
* **runners:** cancel active macos tart jobs before shutdown ([1466ec9](https://github.com/csautter/dev-alchemy/commit/1466ec96b7745d2a9b7a82bb47aeb11f5fb24aab))
* **runners:** normalize shutdown poll interval ([a8a98c8](https://github.com/csautter/dev-alchemy/commit/a8a98c8afe2283255bba79492d65ac63cc408de0))
* **runners:** preserve busy macos tart VMs during shutdown ([089be7b](https://github.com/csautter/dev-alchemy/commit/089be7b19f673288e3fe36fd35a18e5101caefd5))
* **runners:** preserve unknown GitHub API state during cleanup ([75d9fa7](https://github.com/csautter/dev-alchemy/commit/75d9fa763721ac784dee3d8d998d6898e50130de))
* **runners:** report force-cancel failures during shutdown ([81a131c](https://github.com/csautter/dev-alchemy/commit/81a131c23ca308bf088697901c521b19e843f49f))
* **runners:** wait for tart runner deregistration on shutdown ([8c94361](https://github.com/csautter/dev-alchemy/commit/8c9436176fb4bd32bf26b0b03b70ef046965513e))
* **windows:** fall back to alternate cygwin install root ([4de65ac](https://github.com/csautter/dev-alchemy/commit/4de65acd643269af3e203bbd189e6ed527223475))
* **windows:** reinstall cygwin cleanly on version mismatch ([1f13700](https://github.com/csautter/dev-alchemy/commit/1f137002e4e50a388ba536acff9e642112f331c4))
* **windows:** validate Cygwin root before cleanup ([5b7c61c](https://github.com/csautter/dev-alchemy/commit/5b7c61ce6fddc66a239904ff85eaef51cf2ee58a))
* **windows:** verify selected cygwin root before package setup ([6f01d82](https://github.com/csautter/dev-alchemy/commit/6f01d82309e01ecde5f01e00af5965065b403bd1))


### CI

* include role source fixtures in test filters ([27891c6](https://github.com/csautter/dev-alchemy/commit/27891c60a1a279bf7c455a850b5b6ff9d24b82b0))
* validate build cache actions on changes ([b6849cf](https://github.com/csautter/dev-alchemy/commit/b6849cff42556be674dfc85250667a77596695eb))

## [0.15.0](https://github.com/csautter/dev-alchemy/compare/v0.14.0...v0.15.0) (2026-05-11)


### Added

* add oci registry support ([9615e78](https://github.com/csautter/dev-alchemy/commit/9615e7848908c0c44cb34eeb3db126eb92e89c98))
* **oci:** add push and pull list commands ([49ddd64](https://github.com/csautter/dev-alchemy/commit/49ddd64cf8ad84bbf5a1e35fedb31754f328b10e))
* **oci:** add registry push and pull for build artifacts ([3c061f2](https://github.com/csautter/dev-alchemy/commit/3c061f22a10fb3ee223f8f11b69174ec7ce5c3bb))
* **oci:** add TLS options for registry transfers ([e087def](https://github.com/csautter/dev-alchemy/commit/e087defa2600f4bc20ccdfc7e8a154c3b531eb66))
* **oci:** allow confirmed darwin/linux foreign artifact pulls ([a4786a1](https://github.com/csautter/dev-alchemy/commit/a4786a1ff7cbf594d175bcbaae532a9ff79fcad2))
* **oci:** show transfer progress for push and pull ([2e649a0](https://github.com/csautter/dev-alchemy/commit/2e649a0a563408d04563e1dcd98d41a5fca9845f))


### Fixed

* **ci:** restrict GHCR publish permissions ([2a6f990](https://github.com/csautter/dev-alchemy/commit/2a6f9900976516c44886cc6f0f97ffeb4c0f3cfe))
* **cli:** remove duplicated root help sections ([22fd4f1](https://github.com/csautter/dev-alchemy/commit/22fd4f1ccaaaaaeb7e698a744644523c6ab7b006))
* **oci:** reject duplicate manifest layer titles ([8f389b9](https://github.com/csautter/dev-alchemy/commit/8f389b923977dd121a9b0eb76dbe6d51ebad1685))
* **oci:** suppress gosec G402 on insecure TLS field ([c5d970d](https://github.com/csautter/dev-alchemy/commit/c5d970d6a383870b00bc3ee895252425180392da))
* **test:** normalize workflow line endings for Windows CI ([43f77a0](https://github.com/csautter/dev-alchemy/commit/43f77a0bfccf7f80cb90f8a44ebf0a0a3f918c6a))


### Changed

* **oci:** split artifact handling into focused modules ([850ef4f](https://github.com/csautter/dev-alchemy/commit/850ef4f9dd9df3503f627fae805b4481d74bff1c))


### CI

* publish ubuntu build artifacts to GHCR ([b0ed47d](https://github.com/csautter/dev-alchemy/commit/b0ed47d81af7a6482b5ba6b4fa1fceb41f00d388))
* restrict GHCR artifact publishing to main ([c74a1e2](https://github.com/csautter/dev-alchemy/commit/c74a1e2ccc53930abb51b59bde06accedd8a19d9))
* run Linux Windows QEMU builds as start-only probes ([83acc0e](https://github.com/csautter/dev-alchemy/commit/83acc0efc276d02ca31114e2335e88576290e563))
* run OCI package unit tests ([ca1490f](https://github.com/csautter/dev-alchemy/commit/ca1490f4ff8404f7eae7b9838179e3b45ee12372))
* run OCI registry integration test ([23e8781](https://github.com/csautter/dev-alchemy/commit/23e87812471eb6e5fcaa8ad5d80c6383daa88c78))

## [0.14.0](https://github.com/csautter/dev-alchemy/compare/v0.13.0...v0.14.0) (2026-05-09)


### Added

* add windows builds an vms for linux hosts ([eb7f440](https://github.com/csautter/dev-alchemy/commit/eb7f4401f2a15f3f6ba1220beb79a967515bd9cb))
* added linux create vm command ([931cae0](https://github.com/csautter/dev-alchemy/commit/931cae0c1a13c68c9a3c9ee5a527abc220faffb3))
* **build:** add Linux QEMU support for Windows 11 guests ([36097fc](https://github.com/csautter/dev-alchemy/commit/36097fcc1600b3cb1795f8dc35d7c10fc2566db7))
* **build:** select fastest virtio-win download source ([3d0d40f](https://github.com/csautter/dev-alchemy/commit/3d0d40f435028cbee2adb9b56a272e148439fa80))
* **build:** stream VNC snapshots directly to mp4 ([0cc1a7d](https://github.com/csautter/dev-alchemy/commit/0cc1a7de50fbfcd18cc94496fb96b6c84a14ed32))
* **deploy:** add libvirt workflow for Ubuntu QEMU VMs on Linux ([44fcda8](https://github.com/csautter/dev-alchemy/commit/44fcda89b0c39d61d8b3d1032f0bf2cdc957948e))
* **provision:** support Ubuntu provisioning on Linux libvirt ([9f21d04](https://github.com/csautter/dev-alchemy/commit/9f21d04a9bab847af709ca17f5c13c30d146f89b))


### Fixed

* **build:** ensure Windows 11 amd64 QEMU downloads virtio ISO ([a3b3dc5](https://github.com/csautter/dev-alchemy/commit/a3b3dc58a952e6438e27148abb3e712c1ec9c971))
* **build:** extend Linux QEMU build timeouts to five hours ([9ba19ce](https://github.com/csautter/dev-alchemy/commit/9ba19cec2b1cff213ae5743024cbc0687aa495a0))
* **build:** extract Windows ARM64 ISO with UDF-capable tools ([447ce84](https://github.com/csautter/dev-alchemy/commit/447ce841c527929ab83ada3ab20e986f808228d4))
* **build:** install QXL driver during Windows QEMU builds ([799e130](https://github.com/csautter/dev-alchemy/commit/799e130add24e2e75083ae6d228608b64bc51800))
* **build:** keep Ubuntu ISO checksums in sync ([02a86e3](https://github.com/csautter/dev-alchemy/commit/02a86e32ac33e10ed9849ac714a587540e4a76c5))
* **build:** preserve cached artifacts during no-cache builds ([dff2459](https://github.com/csautter/dev-alchemy/commit/dff2459b4192713d62cefa02ab70e245fafafbd3))
* **build:** retry virtio-win download from fallback sources ([37c70e9](https://github.com/csautter/dev-alchemy/commit/37c70e94132f5af6d96ae33da07f59b158b5f65f))
* **build:** shorten macOS Packer start-only temp paths ([40ec4d1](https://github.com/csautter/dev-alchemy/commit/40ec4d13645c8749d5ccb14fe5dfe8f51ec72e46))
* **build:** verify virtio-win ISO checksum ([d4acd37](https://github.com/csautter/dev-alchemy/commit/d4acd37c520233f4f5582727a12b842a2fa3ddaf))
* **ci:** accept Homebrew formula revisions ([56a94f5](https://github.com/csautter/dev-alchemy/commit/56a94f522fab8a565d5435d88b57172c437979a5))
* **ci:** accept newer Homebrew dependency versions ([cf41138](https://github.com/csautter/dev-alchemy/commit/cf411380b8e679affd002787761b364e7c01d2b2))
* **ci:** keep Ubuntu ISO cache references in sync ([80444ec](https://github.com/csautter/dev-alchemy/commit/80444ec3fa223ee17b118e56f19d96faa18f48f9))
* **cli:** mark cross-arch VM targets unstable ([4ee93ea](https://github.com/csautter/dev-alchemy/commit/4ee93eaad85ec55f04f2ddd73ec9ff874941cf22))
* **deploy:** configure QXL video memory for Windows 11 libvirt guests ([d60a591](https://github.com/csautter/dev-alchemy/commit/d60a5917711275a4c2a727e0f4ccc01db7e21b1c))
* **deploy:** harden libvirt image directory access ([8aa0033](https://github.com/csautter/dev-alchemy/commit/8aa00330b875338d57e8e5368e520ee1df1a7a3c))
* **deploy:** keep libvirt ACL hints POSIX on Windows ([075404c](https://github.com/csautter/dev-alchemy/commit/075404c6e48567c0256e16d986db8f5fe7dca7c2))
* **deploy:** make emulated arm64 libvirt domains portable ([cb34c74](https://github.com/csautter/dev-alchemy/commit/cb34c747d07c5d6e78b51692784d4faeff95e4ee))
* **deploy:** preflight libvirt named networks ([f886bf7](https://github.com/csautter/dev-alchemy/commit/f886bf7628b23b68144ba13087fe622a7007df61))
* **kubectl:** clean up apt key temp dir on setup failure ([1a58b5e](https://github.com/csautter/dev-alchemy/commit/1a58b5edc056958588a13572fb1673d5b8dd5a50))
* **kubectl:** make Debian apt key setup resilient ([1efa8cc](https://github.com/csautter/dev-alchemy/commit/1efa8cc04a7b3608a2c5a77cc77e4b4fcd024a10))
* **libvirt:** install QXL driver for Windows auto-resize ([271a76d](https://github.com/csautter/dev-alchemy/commit/271a76deb713f53652250dfb6293dce9df33b51d))
* **linux:** stabilize Ubuntu desktop QEMU/libvirt networking and desktop image setup ([f3de076](https://github.com/csautter/dev-alchemy/commit/f3de076478c15928dad55c86571b17728cb6f89b))
* **macos:** detect latest installed Homebrew version ([1ab1fff](https://github.com/csautter/dev-alchemy/commit/1ab1fffa732e18cce13ed27855400fcd2e8c826c))
* **macos:** pin Homebrew tooling installs ([de21704](https://github.com/csautter/dev-alchemy/commit/de217045981b82f0f62d6844425a8a197a624c33))
* **macos:** warn on unavailable pinned brew versions ([9701275](https://github.com/csautter/dev-alchemy/commit/9701275093a024e0241ed78b71e2061a4f8f841e))
* **packer:** restore persistent DHCP config for QEMU Ubuntu images ([072a676](https://github.com/csautter/dev-alchemy/commit/072a6761a3ff6a93794a83c0483d3865aa6167e8))
* **packer:** simplify QEMU autoinstall networking ([261d1da](https://github.com/csautter/dev-alchemy/commit/261d1da193298616639ce7fb32b83740ec5e68bf))
* **packer:** stabilize ARM64 Ubuntu QEMU installs ([f592a10](https://github.com/csautter/dev-alchemy/commit/f592a10f304595bb80682b289da42f9e9b3e6ea1))
* **provision:** preflight sshpass for password SSH auth ([0409721](https://github.com/csautter/dev-alchemy/commit/0409721fef13ceec2804b46b3827e0ae2ecd1b62))
* **qemu:** tune cross-arch emulation CPU settings ([618b13c](https://github.com/csautter/dev-alchemy/commit/618b13c53c761faa6498f035a2dc0b2b0ad3943b))
* **security:** harden VNC recording file and process handling ([069461e](https://github.com/csautter/dev-alchemy/commit/069461e57e08bfa3261c7cb1fb86a3dd650d99ee))


### Changed

* **build:** extract VNC recording logic into dedicated file ([49dadec](https://github.com/csautter/dev-alchemy/commit/49dadec069aacd9354e9f4e4f989dcf56a9d4de3))
* **packer:** unify Ubuntu QEMU templates ([ff189e1](https://github.com/csautter/dev-alchemy/commit/ff189e1e6526cd84873f6717799848cf625b12a6))


### CI

* add shellcheck workflow ([05eb93c](https://github.com/csautter/dev-alchemy/commit/05eb93cdf377e5e2ea96f7e4df3ba4b940736075))
* default macOS build tests to GitHub-hosted runners ([23adc30](https://github.com/csautter/dev-alchemy/commit/23adc30e225c01186b7ae3bce3cc6c2863bf1f83))
* run Windows 11 QEMU builds in Linux workflow ([ef832e1](https://github.com/csautter/dev-alchemy/commit/ef832e13817d747a0ac4d44d25d84fc7b2f6cb8e))
* set up Go before macOS build dependencies ([d02e9fe](https://github.com/csautter/dev-alchemy/commit/d02e9fe74230f99bd509a6c60ecbbd728bf0b8b4))
* shorten macOS Ubuntu packer build checks ([ad680c0](https://github.com/csautter/dev-alchemy/commit/ad680c09aafba667f469c0e904836a306eede1c7))
* trigger linux build on deploy runtime changes ([876cd00](https://github.com/csautter/dev-alchemy/commit/876cd00f33e25e0a199a87d495e12c1bc9505ca4))

## [0.13.0](https://github.com/csautter/dev-alchemy/compare/v0.12.0...v0.13.0) (2026-04-24)


### Added

* **windows:** unify runner dependency setup behind self-setup script ([e5aaa4a](https://github.com/csautter/dev-alchemy/commit/e5aaa4abc88796ebc1f33d3f499294cdf7bc4ccf))
* **windows:** unify runner dependency setup behind self-setup script ([8b151a5](https://github.com/csautter/dev-alchemy/commit/8b151a55c7e72e6f7fb8bb51b03172e21fd5378f))


### Fixed

* **ci:** pass repository to self-hosted renovate action ([7e60475](https://github.com/csautter/dev-alchemy/commit/7e604754a64fdf08c27f52d0c6287f1cc54e3445))
* **gha:** restore native go and python in Windows Azure runner images ([1a08df0](https://github.com/csautter/dev-alchemy/commit/1a08df021872e746b5a3bcd52ed8bd85d527009a))
* **provision:** detect Hyper-V Ubuntu guest IP without key-based vagrant ssh ([0cba4c3](https://github.com/csautter/dev-alchemy/commit/0cba4c3c50713484ec9d4ba96083cf64073d4e8c))
* **provision:** ignore IPv6 hostnames in linux vagrant IP discovery ([836fc02](https://github.com/csautter/dev-alchemy/commit/836fc02f84c8ac95e00decd9705e2caa5be23b98))
* **renovate:** resolve packer plugin lookups and set git author ([626671c](https://github.com/csautter/dev-alchemy/commit/626671c4e12bd525f45b4a27240fd1cf211c98ad))
* **stop:** use Hyper-V graceful shutdown for Ubuntu VMs before force halt ([89f99b9](https://github.com/csautter/dev-alchemy/commit/89f99b93f41d8d6e63ea8fab67d14018fb987784))
* **windows:** install the correct Chocolatey Python package ([fde8260](https://github.com/csautter/dev-alchemy/commit/fde826047232f529ca0c4b4073c3bbf9500b43ca))
* **windows:** make Go setup opt-in for dependency installs ([ccdb8f2](https://github.com/csautter/dev-alchemy/commit/ccdb8f20f03f6a4345a5ee0ef7f1221eb419a903))


### CI

* **renovate:** use branch config for workflow-dispatch runs ([11e6af7](https://github.com/csautter/dev-alchemy/commit/11e6af717e837269f21a06c1357e010472fd3993))

## [0.12.0](https://github.com/csautter/dev-alchemy/compare/v0.11.0...v0.12.0) (2026-04-21)


### Added

* add build features for linux ubuntu hosts ([911ed61](https://github.com/csautter/dev-alchemy/commit/911ed613421020d6ed28a920bf72bbf10e4cda0e))
* **build:** add Linux-host QEMU builds for Ubuntu server and desktop ([364d308](https://github.com/csautter/dev-alchemy/commit/364d308b03423117b5488d434643036b74f7fcfa))
* **build:** add verbose flag for packer builds ([c4a8c63](https://github.com/csautter/dev-alchemy/commit/c4a8c63f044d17baedcec533c777341e788b40ba))
* **build:** enable VNC video capture for Linux host builds ([3d65997](https://github.com/csautter/dev-alchemy/commit/3d65997b6bd4b16a03af250f688d1a69bb0c3241))
* **build:** show artifact status in build list ([64bb53a](https://github.com/csautter/dev-alchemy/commit/64bb53a2bf62760674f7dba97d9307c9c19a30dc))
* **ci:** add in-repo Hetzner self-hosted runner workflow for Linux builds ([fc88683](https://github.com/csautter/dev-alchemy/commit/fc88683f31dec72a868b7410ba862deb7a5a8fe0))
* **cli:** add optional Go install flag and Linux host installer ([44bebc0](https://github.com/csautter/dev-alchemy/commit/44bebc0b74059657d965b58dbaae338f114c43f2))


### Fixed

* **build:** clean failed artifacts and repair arm64 UEFI extraction ([7da46f8](https://github.com/csautter/dev-alchemy/commit/7da46f8c5372265889883a786339c7d9cd295c6f))
* **build:** clean up incomplete artifacts after failed builds ([34988e7](https://github.com/csautter/dev-alchemy/commit/34988e74d28f10ae09c79dbbf3ae0ffa7462860c))
* **build:** handle dependency downloads without progress bars ([d11a8e9](https://github.com/csautter/dev-alchemy/commit/d11a8e92e49958d83c32979afdceb3499a727493))
* **build:** harden unix tty fd handling for gosec compliance ([5cfa18c](https://github.com/csautter/dev-alchemy/commit/5cfa18c4215b82d0bdc968334c27e538f695e059))
* **build:** improve interrupt cleanup and preserve ffmpeg on graceful cancel ([57b254f](https://github.com/csautter/dev-alchemy/commit/57b254fe3a4b9c5751a516312c7bfe70b1f62fc4))
* **build:** preserve successful artifacts after build cleanup ([26a1ec9](https://github.com/csautter/dev-alchemy/commit/26a1ec97fe11dca2815fcee232c8008e0e572e2b))
* **build:** preserve sudo prompts while restoring helper process logs ([82155e9](https://github.com/csautter/dev-alchemy/commit/82155e988c0f62f854a8d36922a00686d4629754))
* **build:** quote Ubuntu QCOW2 export paths in packer templates ([46fe595](https://github.com/csautter/dev-alchemy/commit/46fe5951877b9598518f7f017857f631b36ae801))
* **build:** tune QEMU CPU and memory settings for Ubuntu VMs ([bd3913c](https://github.com/csautter/dev-alchemy/commit/bd3913c3a8c620ef2a36d232988a829eb88e80fb))
* **github-action:** enable IPv6 and disable IPv4 by default for Hetzner runner ([de96703](https://github.com/csautter/dev-alchemy/commit/de967031609d2ab422c2b47093c275ad803286da))
* **kubelogin:** authenticate GitHub release lookup in CI ([e2cd924](https://github.com/csautter/dev-alchemy/commit/e2cd9242cb08478a37e7777aca45aaf6c74fed85))
* **make:** remove brittle pkill cleanup after build tests ([9c5c0da](https://github.com/csautter/dev-alchemy/commit/9c5c0dab03dc5268332b411ddb7adeddbca328e7))
* **packer:** move qemu ubuntu package installs out of cloud-init ([66ee86b](https://github.com/csautter/dev-alchemy/commit/66ee86ba471a119dd1839d189d423841c6a1c116))
* **packer:** simplify ubuntu desktop qemu provisioning ([cfbd97c](https://github.com/csautter/dev-alchemy/commit/cfbd97c7fa0e360ed00aaf21d5335d6ed44cbbcb))
* **provisioning:** install ansible in devcontainer and fix kubelogin header templating ([4786109](https://github.com/csautter/dev-alchemy/commit/47861098309d6ea02f7e9690e8863b0a37592ea0))


### Changed

* **packer:** split linux and macos ubuntu qemu build paths ([6dbfc44](https://github.com/csautter/dev-alchemy/commit/6dbfc442b6a5d25bef6162782ab4fae094869c03))


### CI

* add cold-cache dependency bootstrap coverage to Linux workflow ([9511385](https://github.com/csautter/dev-alchemy/commit/9511385ffd5d66b18e6daec76c90ed31ccab164f))
* add native Linux QEMU smoke builds for amd64 and arm64 ([fe056bb](https://github.com/csautter/dev-alchemy/commit/fe056bb3400dd96230c1d6b30b65751a9cae41eb))
* broaden build workflow path filters for shared build inputs ([f6bf616](https://github.com/csautter/dev-alchemy/commit/f6bf616df1c6c4a2805182cc095b17b014cd2b0f))
* **deps:** schedule Renovate in GitHub Actions ([86f5734](https://github.com/csautter/dev-alchemy/commit/86f5734a7d27d81cfc5a6d98801a1d6ae70e096f))

## [0.11.0](https://github.com/csautter/dev-alchemy/compare/v0.10.0...v0.11.0) (2026-04-05)


### Added

* provision local windows ssh provisioning ([5115d6a](https://github.com/csautter/dev-alchemy/commit/5115d6ae30f7ae163f5922704360d54f906e1b69))
* **provision:** add local Windows SSH provisioning with force uninstall cleanup ([f11ad4b](https://github.com/csautter/dev-alchemy/commit/f11ad4bb985f4cda02ca2ced5b76e610f4234346))


### Fixed

* **ci:** trigger PR workflows on cmd/main.go changes ([459c297](https://github.com/csautter/dev-alchemy/commit/459c2971d6da37e1e37e5fa0c0a844426ea52a0e))
* **provision:** avoid reboot-only local Windows SSH cleanup and log OpenSSH install progress ([b480986](https://github.com/csautter/dev-alchemy/commit/b48098658d124ebde491759fe9b29685f9205b27))
* **provision:** guard SSH wire length encoding against uint32 overflow ([ec9ef74](https://github.com/csautter/dev-alchemy/commit/ec9ef749d673c324177032d7685004ebdcb821e0))
* **provision:** harden local Windows SSH bootstrap state handling ([e2e187e](https://github.com/csautter/dev-alchemy/commit/e2e187e6996b7f79e3ad5112c399e1fa00829cf1))
* **provision:** harden local Windows SSH port selection and cleanup ([725b827](https://github.com/csautter/dev-alchemy/commit/725b827114748b41e04a675e44b172115a1a5f56))
* **provision:** improve local Windows SSH auth setup, cleanup, and preflight diagnostics ([f2572a2](https://github.com/csautter/dev-alchemy/commit/f2572a2c690b1b6bace3391ba5340fd01ea83c08))
* **provision:** use a dedicated local Windows SSH port and detect wrong SSH listeners ([db65e64](https://github.com/csautter/dev-alchemy/commit/db65e64f579ece5f6d94393721b967746517cefd))
* **windows:** preserve existing ssh provision user on force uninstall ([e7a643c](https://github.com/csautter/dev-alchemy/commit/e7a643ccfa3270ba93d61a605fec19bfd6640154))


### Changed

* **provision:** deduplicate local windows session runner flow ([786e286](https://github.com/csautter/dev-alchemy/commit/786e286a36f5e71793e4fde2f7aca2980f6a8134))
* **provision:** share managed Windows user lifecycle across ssh and winrm ([3a685d7](https://github.com/csautter/dev-alchemy/commit/3a685d704458aa1fae89964629eeac4ae0a161e1))


### CI

* exercise Windows local provision flows in ansible role tests ([95588e9](https://github.com/csautter/dev-alchemy/commit/95588e99e172e69be7dd41621da25237fef146a1))
* remove Windows WinRM setup from ansible role test workflow ([0aef486](https://github.com/csautter/dev-alchemy/commit/0aef486c28d34b42bebc1d1fbdf21cebd04ff566))

## [0.10.0](https://github.com/csautter/dev-alchemy/compare/v0.9.2...v0.10.0) (2026-04-04)


### Added

* **provision:** add generic ansible arg passthrough and inventory override ([0d2f284](https://github.com/csautter/dev-alchemy/commit/0d2f2842f3d4b09aa6935399c4b5ec34296f73a8))
* **provision:** add local host provision target ([833e077](https://github.com/csautter/dev-alchemy/commit/833e0776371e1beb42cff2432aee2b38208d3f79))
* **provision:** add local host provisioning with Windows WinRM bootstrap ([521aa46](https://github.com/csautter/dev-alchemy/commit/521aa464c5be6c6c012f8750a3ce879da495751f))
* **provision:** allow overriding ansible playbook path ([71f74a3](https://github.com/csautter/dev-alchemy/commit/71f74a3b63d91d2b48d295e1ab31385bb1a399cc))
* **provision:** secure local Windows WinRM provisioning ([ba8f592](https://github.com/csautter/dev-alchemy/commit/ba8f5928050965d821d7d77c550e66d135883691))
* restructure docs ([585b871](https://github.com/csautter/dev-alchemy/commit/585b8713c75ab8d24cc79397810344a03acd22f8))


### Fixed

* **ci:** run Windows winrm role test via provision local ([3585a69](https://github.com/csautter/dev-alchemy/commit/3585a69eb5593ba944b9bcc617222873336261b1))
* **provision:** bind local Windows WinRM bootstrap to loopback ([768eeed](https://github.com/csautter/dev-alchemy/commit/768eeedd337f13610d8f6bb8d5bd450d54fb2b3a))
* **provision:** elevate and confirm local Windows WinRM bootstrap ([dc0adaa](https://github.com/csautter/dev-alchemy/commit/dc0adaa77dce00cc13ff4b3db99824af964fb5ea))
* **provision:** enforce documented ansible verbosity range ([5d1dd8a](https://github.com/csautter/dev-alchemy/commit/5d1dd8a6a3b19fa49c052d5839645d2ba165c723))
* **provision:** harden local Windows WinRM bootstrap cleanup ([310d4f1](https://github.com/csautter/dev-alchemy/commit/310d4f123bdb5e3be663edc7602fadeeae4d0c92))
* **provision:** log local Windows PowerShell bootstrap and cleanup progress ([146a8f1](https://github.com/csautter/dev-alchemy/commit/146a8f1210d1c0596dea72e3981b67b6981576ba))
* **provision:** skip Windows RunAs hop in elevated shells ([42ce281](https://github.com/csautter/dev-alchemy/commit/42ce2815c8d0f65417a61f7f231696040484a0b0))
* **provision:** use HTTPS-only local WinRM bootstrap on Windows ([a1fe0e7](https://github.com/csautter/dev-alchemy/commit/a1fe0e7e4644336a132068c61210dc4c44e1a8fe))
* **security:** triage and suppress provision gosec findings ([a528c99](https://github.com/csautter/dev-alchemy/commit/a528c99c09de1ecece4b1fe5372488c1b2d34f98))


### Changed

* **provision:** extract local Windows WinRM scripts into ps1 assets ([60971c8](https://github.com/csautter/dev-alchemy/commit/60971c80b58a5e5c8dcdf999f4ec56fab1dd6966))
* **provision:** move windows local logic into dedicated file ([80c6895](https://github.com/csautter/dev-alchemy/commit/80c68954144ab64974b680a7fabbe969b790d207))


### CI

* run full cmd package tests in deploy unit workflow ([c8f1880](https://github.com/csautter/dev-alchemy/commit/c8f18804a4e3344ed7cd62822556ea866046a6e8))

## [0.9.2](https://github.com/csautter/dev-alchemy/compare/v0.9.1...v0.9.2) (2026-03-30)


### Fixed

* **build:** skip unchanged embedded project sync and prune stale assets ([c8ddc24](https://github.com/csautter/dev-alchemy/commit/c8ddc24f0a5d714cb7b94d15fdf715af97285559))


### CI

* reuse install scripts in build workflows and make oscdimg setup idempotent ([6c9f033](https://github.com/csautter/dev-alchemy/commit/6c9f033c7f75361161aef84c4bd8e7f08e341200))
* reuse install scripts in build workflows and make oscdimg setup idempotent ([b10300c](https://github.com/csautter/dev-alchemy/commit/b10300cf302d0f251ad60dfe8844dc42b5b74bb1))

## [0.9.1](https://github.com/csautter/dev-alchemy/compare/v0.9.0...v0.9.1) (2026-03-29)


### Fixed

* **ci:** specify repository for gh release upload ([f693ae1](https://github.com/csautter/dev-alchemy/commit/f693ae1277228050c7df9f9f64d9dca98f7addc7))

## [0.9.0](https://github.com/csautter/dev-alchemy/compare/v0.8.0...v0.9.0) (2026-03-29)


### Added

* embed runtime assets for standalone CLI builds and release binaries ([b8d7506](https://github.com/csautter/dev-alchemy/commit/b8d7506db767d1f1ce00cc01cf0981329869b43c))
* embed runtime assets for standalone CLI builds and release binaries ([af5d159](https://github.com/csautter/dev-alchemy/commit/af5d159650060283e88d6d6ae908d359b2e1421c))


### Fixed

* **build:** harden embedded project extraction against gosec findings ([b850868](https://github.com/csautter/dev-alchemy/commit/b8508683e7233e3e2d5e246a6aa3048c43cf179f))
* **build:** stop external process retries immediately on interrupt ([24bf8da](https://github.com/csautter/dev-alchemy/commit/24bf8da8c02cfaa26d1c9b0cbbe54792d9c92296))
* **build:** write Windows release zip to dist for artifact upload ([ca05356](https://github.com/csautter/dev-alchemy/commit/ca0535682c2dc7dc0c8feebe881a0773c8f58b96))
* **deps:** upgrade google.golang.org/grpc to v1.79.3 for GO-2026-4762 ([9942bd7](https://github.com/csautter/dev-alchemy/commit/9942bd7327db700b97c6da12b078f08bd8a3e02f))


### CI

* run release binary build dry-runs in pull requests ([4a0a132](https://github.com/csautter/dev-alchemy/commit/4a0a1320dd3291a3ab5e9da8af0abdb4f6be9884))
* store Hyper-V diagnostics in private Azure cach ([9e4d904](https://github.com/csautter/dev-alchemy/commit/9e4d9045c1fab0d301f12b8e1598e01d47d42206))

## [0.8.0](https://github.com/csautter/dev-alchemy/compare/v0.7.0...v0.8.0) (2026-03-29)


### Added

* enhaned debugging for hyperv runner ([90ce346](https://github.com/csautter/dev-alchemy/commit/90ce3464f342f01a66b988f0f54c856aac991ef2))


### Fixed

* **build:** add engine selection and mark virtualbox unstable ([8434021](https://github.com/csautter/dev-alchemy/commit/8434021c904b737eb38826c98b84dfbdea48a12a))
* **build:** exclude unstable virtualbox target from build all by default ([0876478](https://github.com/csautter/dev-alchemy/commit/087647888cd14ce7d18115c6577bce492ce7185f))
* **build:** restrict managed app directory permissions to 0700 ([2bc4f7d](https://github.com/csautter/dev-alchemy/commit/2bc4f7de660dd7112ce6bf85a5ed759c916a8c65))
* **build:** shorten macos qemu output paths and stop noisy vnc retries ([20354ec](https://github.com/csautter/dev-alchemy/commit/20354ec011cd5d5d394e091e622061f1814971d8))
* **ci:** align macOS workflow cache paths with managed app data ([0e08270](https://github.com/csautter/dev-alchemy/commit/0e082708048154c32f3b4e0e91de756805f04aba))
* **ci:** align Windows workflow cache paths with managed app data ([c0e419a](https://github.com/csautter/dev-alchemy/commit/c0e419a43d7d0de214d07fc36733ac415a89d8a0))
* **ci:** capture sanitized Hyper-V diagnostics for Windows build failures ([bfb7246](https://github.com/csautter/dev-alchemy/commit/bfb72463ccb442f26980fb00b1c3996d45395571))
* **ci:** harden Playwright Windows ISO fetch and upload diagnostics ([99b0cf3](https://github.com/csautter/dev-alchemy/commit/99b0cf390348d4d4c09ee48165273a0e0c1ba099))
* **packer:** use valid validation messages for cache_dir ([aa924f4](https://github.com/csautter/dev-alchemy/commit/aa924f426b3f23c29ec46ef31797c53e9a062dde))


### Changed

* move VM state into managed app data ([9697909](https://github.com/csautter/dev-alchemy/commit/9697909783a3b774a782a3706e17dfb52261b437))
* move VM state into managed app data ([1077820](https://github.com/csautter/dev-alchemy/commit/1077820edb0fcdb1e7e72f5a57134c13420bf4b9))


### CI

* refresh Azure auth before HyperV queue cleanup ([9189f40](https://github.com/csautter/dev-alchemy/commit/9189f400c91c24a464e8c0c8ef6cd76823f8c00a))
* run deploy and provision unit tests on main pushes ([23dc24f](https://github.com/csautter/dev-alchemy/commit/23dc24ffff8f76e7da0ccde7d6dad99d063181c9))
* run gitleaks and cmd unit tests on main pushes ([4db3625](https://github.com/csautter/dev-alchemy/commit/4db3625756827014e0a124f8b4594b6c8c1d1f6c))

## [0.7.0](https://github.com/csautter/dev-alchemy/compare/v0.6.0...v0.7.0) (2026-03-28)


### Added

* **cli:** add destroy command for managed VMs ([490901b](https://github.com/csautter/dev-alchemy/commit/490901bea7816f307a477889b29ff8017ac2ae53))
* **create:** detect existing VM targets before deploy ([5ab8087](https://github.com/csautter/dev-alchemy/commit/5ab808707132864dd736b0f6722c25371d7ec334))
* **destroy:** add VM destroy readiness listing and tart state fixes ([05e3175](https://github.com/csautter/dev-alchemy/commit/05e31751954c7905c98045fd6971eda461eb9686))
* **vm:** add lifecycle commands for starting, stopping, and destroying managed VMs ([529adfa](https://github.com/csautter/dev-alchemy/commit/529adfa3ca28c7a45377cefc18217bfa5f1fa6fd))
* **vm:** add start command and fail-fast provision preflight ([1cb7a7a](https://github.com/csautter/dev-alchemy/commit/1cb7a7a593827bfe9b1deba4bab43b2a12bc2223))
* **vm:** add stop command with graceful UTM shutdown ([79b5689](https://github.com/csautter/dev-alchemy/commit/79b568916c4a5a0924d1519425fe2dfda2313bed))


### Fixed

* **deploy:** harden Hyper-V Vagrant stop with forced halt fallback ([fb75cdb](https://github.com/csautter/dev-alchemy/commit/fb75cdbffacf72353ec50cf56d5c94764ba2bfdb))
* **deploy:** infer canonical UTM targets for macOS deploy configs ([893d616](https://github.com/csautter/dev-alchemy/commit/893d616cace68f17fcdff80145f5c30dad94f11a))
* **deploy:** inspect Hyper-V VM state via PowerShell ([21bd7da](https://github.com/csautter/dev-alchemy/commit/21bd7da2d964006ce4b6c2af87befcc969393f7e))
* **deploy:** make Hyper-V stop timeout boundary deterministic ([36f9499](https://github.com/csautter/dev-alchemy/commit/36f9499547f7e20eefc0dad6cbea616a33f21ffb))
* **deploy:** normalize Hyper-V Vagrant dotfile env paths on Windows ([0aec0d9](https://github.com/csautter/dev-alchemy/commit/0aec0d9f69dc696c0475e110f83223f511b6d5ea))
* **hyperv:** isolate vagrant state per vm and prevent false create detection ([eabb917](https://github.com/csautter/dev-alchemy/commit/eabb9172a7f78034b64b8b886c7e8890c079c6ca))
* **provision:** use VM-specific Hyper-V Vagrant settings ([5c78d8d](https://github.com/csautter/dev-alchemy/commit/5c78d8dd7a425459dd70c863ae82151d4a6ecb40))


### Changed

* **provision:** move VM provisioning logic out of pkg/deploy ([f6cc593](https://github.com/csautter/dev-alchemy/commit/f6cc5937910ba70932123f90a4b88a30989bb37e))


### CI

* add PR coverage for deploy and provision tests ([33dd2a3](https://github.com/csautter/dev-alchemy/commit/33dd2a316600ad176c3501ff605e382fc638549d))

## [0.6.0](https://github.com/csautter/dev-alchemy/compare/v0.5.0...v0.6.0) (2026-03-24)


### Added

* **tart:** add macOS Tart target for create and provision ([e721799](https://github.com/csautter/dev-alchemy/commit/e7217997e0b7672d81347202d7462f454034b905))
* **tart:** add macOS Tart target for create and provision ([e0203c9](https://github.com/csautter/dev-alchemy/commit/e0203c9bb1624ebee751fc0502eb535ec9feef12))


### Fixed

* **ansible:** resolve macOS Java homes on target before jenv add ([a408ef2](https://github.com/csautter/dev-alchemy/commit/a408ef24ee458ceb8f85807d9d4c19d50aa45b18))
* **deploy:** address gosec findings in tart deploy helpers ([b564d3f](https://github.com/csautter/dev-alchemy/commit/b564d3f28cb6c90bc658b6c199dfd7e6f3380d6c))
* **deploy:** handle existing running Tart macOS VMs explicitly ([6ee6e8c](https://github.com/csautter/dev-alchemy/commit/6ee6e8ce06dc4edd1157a61fb4567277ff1491dc))
* **deploy:** parse tart list output by columns for local VM lookup ([715b704](https://github.com/csautter/dev-alchemy/commit/715b70498ed060c91442d635111f23deda6929a9))


### Changed

* **deploy:** extract shared SSH wait helper ([35d32cd](https://github.com/csautter/dev-alchemy/commit/35d32cd503f4733f8692bc88c09b2fccde43f54b))
* rename shared SSH provision arg builder ([f1b0629](https://github.com/csautter/dev-alchemy/commit/f1b0629a6949361a3034d6e255e33c30e2aef729))

## [0.5.0](https://github.com/csautter/dev-alchemy/compare/v0.4.0...v0.5.0) (2026-03-23)


### Added

* **provision:** add Ubuntu UTM provisioning support on macOS ([33dace4](https://github.com/csautter/dev-alchemy/commit/33dace45b6b2e9b32251c32a29f5cf97f8308884))
* **provision:** add Ubuntu UTM provisioning support on macOS ([cc83f38](https://github.com/csautter/dev-alchemy/commit/cc83f381030f2e95fd42a10894faa98375bbee0f))


### Fixed

* **actions:** simplify Hyper-V queue watcher job parsing ([4bbaff8](https://github.com/csautter/dev-alchemy/commit/4bbaff841f83280acac627448eb126f4132bd536))
* **ci:** retry HyperV runner cleanup before canceling queued builds ([a4a710c](https://github.com/csautter/dev-alchemy/commit/a4a710cfb89f85d58fd5dab275851d1d41187e35))


### CI

* cancel Windows Hyper-V test runs when the runner stays queued ([30c9852](https://github.com/csautter/dev-alchemy/commit/30c98526f07c44d4a87a20a58b2c140591c6191c))
* scope Windows workflow actions write permission to queue watcher job ([4d3c23e](https://github.com/csautter/dev-alchemy/commit/4d3c23e20c1aa849e19fb82360cb6ea1df1c60a0))
* trigger macOS VM workflow for provision code changes ([907cc21](https://github.com/csautter/dev-alchemy/commit/907cc21a9047316af0dbbeb987871dbb5e61a924))

## [0.4.0](https://github.com/csautter/dev-alchemy/compare/v0.3.0...v0.4.0) (2026-03-22)


### Added

* **build:** add --no-cache to force rebuilding existing VM artifacts ([13c1783](https://github.com/csautter/dev-alchemy/commit/13c17833328fb144700dc9193552509b71ebd0a2))
* **build:** add list command for available VM build combinations ([023d67f](https://github.com/csautter/dev-alchemy/commit/023d67f9673905b469b03cebe6402d3a2ecebe67))
* **cli:** add install command for host dependencies ([0ffd087](https://github.com/csautter/dev-alchemy/commit/0ffd0877a9f9dec95a39d298f4cb26844849c61d))
* **create:** add create list command and artifact readiness checks ([1fc0bed](https://github.com/csautter/dev-alchemy/commit/1fc0bedfd180ae9b33957d57fea5982892f1d157))
* **provision:** add supported target listing and filter unsupported VMs ([f378c06](https://github.com/csautter/dev-alchemy/commit/f378c064aa14e8cc1a148237ef1ce5b1ee8a50d1))
* **provision:** add UTM Windows 11 provisioning support ([556ce74](https://github.com/csautter/dev-alchemy/commit/556ce749c58af9f2998f9f3f53840e5e1f75c6a0))
* **provision:** add UTM Windows 11 provisioning support ([baacca3](https://github.com/csautter/dev-alchemy/commit/baacca35df2846beb061057978ba17bc3bd4304e))


### Fixed

* address multiple gosec findings ([b3a2cf1](https://github.com/csautter/dev-alchemy/commit/b3a2cf1582e2a5b5060f34cc81115b8744dde930))
* **ci:** restore artifact directory access after gosec hardening ([2855f00](https://github.com/csautter/dev-alchemy/commit/2855f000245dcd7dd1c885b07d831b093f3bec48))
* **ci:** restore cache ownership after sudo macos builds ([fc9e4e2](https://github.com/csautter/dev-alchemy/commit/fc9e4e2edbb7b5445c9ada621e9e9a77931b26e4))
* **deploy:** re-prime UTM ARP cache while waiting for guest IPv4 ([47206e9](https://github.com/csautter/dev-alchemy/commit/47206e9e71426e12e7c7f66b082089997d142b55))
* **deploy:** retry UTM IP discovery and warm ARP cache ([1f40162](https://github.com/csautter/dev-alchemy/commit/1f40162297960058b3c74a269990a32aa87a7e79))
* **windows:** keep WinRM reachable across network profile changes ([00d0dcf](https://github.com/csautter/dev-alchemy/commit/00d0dcfdcdad107945566e8d6321a6edd22d7465))


### Changed

* deduplicate build/create/provision VM list rendering ([75af05e](https://github.com/csautter/dev-alchemy/commit/75af05e8f9c6947018967bf44da1941f2e00a2b8))


### CI

* align Windows and macOS build job timeouts with packer timeouts ([2d72d46](https://github.com/csautter/dev-alchemy/commit/2d72d4659e8523c02e6ed48fdbb42dd44ecf5f2e))
* run cmd and pkg/build unit tests in GitHub Actions ([630b74e](https://github.com/csautter/dev-alchemy/commit/630b74e11932df822c755efa5200f47af25c1181))

## [0.3.0](https://github.com/csautter/dev-alchemy/compare/v0.2.0...v0.3.0) (2026-03-17)


### Added

* **ansible:** add configurable Debian Spotify install with apt/snap fallback ([c5ae866](https://github.com/csautter/dev-alchemy/commit/c5ae866242d7c378f5f054e10a983a25f5c84b80))
* **build:** add Hyper-V Ubuntu support on Windows and restructure cloud-init configs ([eaef86d](https://github.com/csautter/dev-alchemy/commit/eaef86df0f0c57f87fe8830c867a92e536b9ca52))
* **build:** add Hyper-V Ubuntu support on Windows and restructure cloud-init configs ([f99f622](https://github.com/csautter/dev-alchemy/commit/f99f62293c4f02e4c69df7046134d0713b6ba729))
* **hyperv:** parameterize Vagrant VM resources from build config ([ec52f11](https://github.com/csautter/dev-alchemy/commit/ec52f113796b11bb63be6751139f5378dd165a1c))


### Fixed

* **ansible:** make installer roles check-mode safe ([f0b1f37](https://github.com/csautter/dev-alchemy/commit/f0b1f373a1e547723cbbd7daa0d83336d8d484c4))
* **kubectl:** avoid Windows Chocolatey failure when newer kubernetes-cli is already installed ([174b1fd](https://github.com/csautter/dev-alchemy/commit/174b1fdce174a12e89050f9758e8d42cf4055da8))


### CI

* add deploy smoke tests to macOS and Windows build workflows ([93951cf](https://github.com/csautter/dev-alchemy/commit/93951cfbe62fd6bc20fbb0271c76f5f677419749))
* extend cmd unit-test workflow to cover provision and deploy tests ([95a4e50](https://github.com/csautter/dev-alchemy/commit/95a4e50be12377eb164e914ff54fea3cbc9cd8aa))
* **windows:** set test-hyperv-build job timeout to 60 minutes ([d1086c3](https://github.com/csautter/dev-alchemy/commit/d1086c3709e606b0c0de28739cd6011520010e89))

## [Unreleased]

## [0.2.0](https://github.com/csautter/dev-alchemy/compare/v0.1.1...v0.2.0) (2026-03-14)

### Added

#### Windows Hyper-V Vagrant Deploy
- Hyper-V Vagrant deployment path wired into the `create` command when the VM config uses the Hyper-V virtualization engine.
- Hyper-V Vagrantfile now pins a switch via `VAGRANT_HYPERV_SWITCH` to avoid interactive network selection.

#### Provision Command
- Added a unified provisioning command for VM targets: `go run cmd/main.go provision <osname>`.
- Added Windows 11 Hyper-V provisioning flow through the Go wrapper (`pkg/deploy/provision.go`), including `--check` support.

#### Devcontainer
- Added a Go devcontainer definition with Python and Packer features (`.devcontainer/devcontainer.json`).

#### Azure Runner Broker (Function App)
- New Azure Function App (`scripts/gh-runner-func/`) that provisions and tears down self-hosted GitHub runner VMs on demand.
- HTTP endpoint `POST /api/request_runner` — creates runner resource group, network, and VM.
- HTTP endpoint `POST /api/delete_resource_group` — deletes a named resource group after the job completes.
- Terraform/Terragrunt module (`deployments/terraform/modules/azure_gh_runner/`) that deploys the Function App, Key Vault, storage account, virtual network, and all supporting Azure resources.
- Terraform/Terragrunt base structure (`deployments/terraform/`) with root `hcl`, local and Azure backends, and per-environment overlays.
- Function App reads runner registration token and secrets from Azure Key Vault at runtime.
- OIDC-based authentication for GitHub Actions workflows calling Azure — eliminates long-lived `AZURE_CREDENTIALS` secrets.
- Operator README (`scripts/gh-runner-func/README.md`) documenting API contracts, authentication requirements, required app settings, and deployment steps.
- Terraform module outputs for downstream consumers.
- gitleaks secret-scanning CI job to catch credentials in source.

#### Windows Host Build Automation
- Automated VirtualBox Windows 11 VM build path on a Windows host (`pkg/build/windows-build.go`).
- Automated Hyper-V Windows 11 VM build path on a Windows host, including Default Switch configuration.
- Build test CI job targeting a Windows host runner (`.github/workflows/test-build.yml`).
- Ansible role and playbook additions: VirtualBox provisioning for the Windows 11 GitHub runner image.
- Windows runner setup: Bash, Make, and Packer installed and added to `PATH` on the runner.

#### macOS Tart Runner Infrastructure
- Shell script (`scripts/macos/`) to create ephemeral macOS GitHub runners in a loop using Tart VMs.
- Golden image creation pipeline for macOS Tart runners (pre-installs GitHub runner, Homebrew packages, Packer, Go).
- Graceful runner de-registration on script termination.

#### CI: ISO Caching
- Runner ISO cache directory (`./cache/windows11/iso/`) for all Windows 11 build jobs.
- Cache population step on first run per host so subsequent runs skip the download.
- Azure Blob Storage ISO upload and download composite actions (`build/gh_actions/`) for cross-host cache warming.
- `iso-path` output propagated from the download action to downstream build steps.
- Windows 11 ISO download test (`test: added windows11 download test`).

#### CI: Azure VM Build Runners
- Azure VM temp disk (`/mnt`) used as the Packer build workspace to avoid OS disk I/O bottlenecks.
- Spot-instance support for Azure Windows runners with automatic fallback to on-demand pricing.
- Build job opt-in variables for manual workflow dispatches (`VM_USE_SPOT`, `CUSTOM_IMAGE_ID`, Hyper-V / VirtualBox flavor selection).
- Fallback mechanism: if the self-hosted runner label is unavailable, the workflow automatically falls back to a GitHub-hosted runner.

#### CI: Build-Runner Tests
- Comprehensive unit-test suite for `runParallelBuilds` covering 6 scenarios: all succeed (parallelism=2), partial failure with others still running, SIGINT via context cancel, OS SIGINT signal wiring, sequential-all-succeed, and sequential-failure-does-not-skip-remainder (`cmd/cmd/build_parallel_test.go`).
- New GitHub Actions workflow (`.github/workflows/test-build-runner.yml`) that runs the build-runner tests on push/PR changes to `cmd/cmd/build.go`, `cmd/cmd/build_parallel_test.go`, and the workflow file itself.
- `make test-build-runner` Makefile target for running the build-runner unit tests locally.

---

### Changed

#### Deploy Command Runner
- macOS UTM deploy now uses a shared command runner with streamed stdout/stderr and timeouts.
- Hyper-V Vagrant instructions now reference the cache path for the Windows 11 box.
- Hyper-V Windows provisioning now discovers the VM host IP on demand and runs Ansible with an inline host target instead of mutating a tracked inventory file.
- WinRM settings for Hyper-V Windows provisioning are sourced from process environment or project-root `.env`.
- Command logging/error surfaces now redact `ansible_password` values in CLI arguments.

#### CI Workflow Topology
- `test-build.yml` restructured: separate matrix jobs for Hyper-V and VirtualBox flavors; `fail-fast: false` set on the matrix.
- Azure runner region moved to `eastus2`; VM type upgraded to `Standard_D4s_v5` for faster builds.
- Dynamic memory allocation for Packer Win11 builds replaces hard-coded values.
- ISO paths unified to `./cache/windows11/iso/` across all workflow steps (was `vendor/windows/`).
- Blob upload/download steps refactored into reusable composite actions.
- Job dependency graph optimised for faster Azure runner cleanup after builds complete.
- macOS jobs now run on self-hosted Tart-based runners instead of GitHub-hosted macOS.
- Tart runner prepare script simplified; GitHub CLI auth check added before VM build.

#### Build Package (`pkg/build`) / `cmd/cmd`
- Windows build code extracted into dedicated file (`windows-build.go`); generic helpers moved to `generic_build.go`.
- `checkIfBuildArtifactsExist` function extracted for reuse.
- Build script handling refactored into smaller, focused functions.
- VNC recording config now passed by reference.
- Windows ISO path constant updated to `./cache` directory.
- `runParallelBuilds` extracted to a standalone, context-aware function in `cmd/cmd/build.go`; errors from individual VM builds are now collected and reported (with VM identity) instead of being silently discarded.

#### Hyper-V Setup
- Setup logic refactored; PowerShell provisioning script extracted from inline Python code.
- Restart sequence corrected; existing-resource handling hardened.

#### Terraform / Infrastructure
- Azure storage account creation moved into the Terraform module (removed from GitHub Actions workflow steps).
- Custom VM names and resource group names passed explicitly from CI to the Function App.
- Key Vault name passed to the Function App via environment variable.

#### Documentation
- `.github/runners/README.md` ISO path references updated from `vendor/windows/` to `cache/windows11/iso/`.
- VirtioFS mount path corrected from `/Volumes/iso-cache/` to `/Volumes/My Shared Files/iso-cache/` to match the actual Tart `--dir` mount name.

---

### Fixed

- Deadlock in `stopVncScreenCaptureOnMacosDarwin`: non-blocking channel send now used when the
  VNC goroutine has already exited on a successful vncsnapshot run.
- Build hanging after all VMs complete: `RunExternalProcessWithRetries` previously returned
  `context.Background()` (never done) on retry exhaustion; now returns a cancelled context so
  dependents unblock correctly.
- SIGINT/SIGTERM during vncsnapshot retry-interval sleep no longer causes a hang; the sleep
  is now interruptible via signal.
- Removed hardcoded `-k de` (German keyboard layout) from QEMU args that caused incorrect
  inputs in the `boot_command` sequence on non-German systems.

#### Security & Auth
- **Azure Function auth guard hardened**: JWT-based validation now enforced; dead key-based auth code removed (`fix(auth): harden function app auth guard`).
- **Input validation added** to Function App endpoints; error responses no longer leak internal stack traces or infrastructure detail (`fix(security): validate inputs and prevent error detail leakage`).
- OAuth2 scope added and Azure CLI pre-authorized on the app registration (`fix(auth): add OAuth2 scope and pre-authorize Azure CLI on app registration`).
- Function App endpoint secured so only Azure CLI / OIDC-authenticated callers can invoke it.
- OIDC subject claim fixed for pull request workflows (`fix: oidc subject for gh prs`).
- Azure login step added to the CI cleanup job so resource group deletion succeeds when credentials rotate.

#### Build & Runtime
- Packer `init` errors in the Windows build path are now propagated and fail fast instead of being silently swallowed (`fix(build): propagate packer init errors in Windows build path`).
- Dependency bootstrap failures (venv creation, pip install, Playwright setup) now propagate immediately with context instead of continuing with a broken environment (`fix(build): propagate dependency bootstrap failures immediately`).
- Duplicate `cmd.Wait()` call removed — eliminated spurious `"wait: no child processes"` errors in long-running build processes.
- Azure VM name truncated to 15 characters to satisfy Windows NetBIOS naming constraint.
- Hyper-V Default Switch IP race condition mitigated with retry logic on early build failures.
- Hyper-V Windows 11 boot command timing corrected.
- Max CPU core count capped at 2 to respect hypervisor limits; CPU count variables added to VirtualBox and Hyper-V configs.
- Playwright `stealth` plugin installed during dependency reconciliation.
- Incomplete Playwright downloads cleaned up before retry.
- Playwright Win 11 download approach reverted to stable method.
- Windows ISO download skips re-download if the file already exists.
- PowerShell log file encoding fixed to UTF-8.
- `HostOs` field set correctly for macOS VM definitions.
- Blocked channel removed from build pipeline.

#### CI
- `go mod vendor` run before `go test` to ensure vendored dependencies are present on the runner.
- Go test invocation changed from file path to package path (e.g. `./pkg/build/...`).
- `go test` on macOS build jobs now runs with `sudo` when required.
- Workflow trigger path filter updated to reference the current workflow file (was pointing to a deleted file).
- Azure Blob upload step now checks for file existence before attempting upload.
- ISO download step no longer masks errors from `az storage blob download`.
- Permissions added to the blob upload job (`id-token: write`, `contents: read`).
- macOS Tart ephemeral runners safely removed after use.
- Runner label shortened to satisfy GitHub runner name length limit.
- `az rest` CLI command updated with `--resource` flag.
- Python setup fixed for custom GitHub Actions Windows runner.

#### Terraform
- Terraform `random` integer range restored to a meaningful spread with a `lifecycle { ignore_changes }` guard to prevent resource replacement on re-apply (`fix(terraform): restore random suffix range with stable lifecycle guard`).

---

### Security

> The following items were addressed in this branch; open items are tracked as follow-up work.

| # | Finding | Status |
|---|---------|--------|
| 1 | Function App reachable anonymously (`unauthenticated_action = "AllowAnonymous"`) | **Partially mitigated** — auth guard hardened; `unauthenticated_action` enforcement tracked as follow-up |
| 2 | Auth gate validated only on header *presence*, not JWT claims | **Fixed** — JWT-based validation enforced; dead code removed |
| 3 | Function identity holds subscription-wide `Contributor` | **Open** — least-privilege scoping tracked as follow-up |
| 4 | RDP port 3389 open to `*` in NSG | **Open** — tracked as follow-up |
| 5 | Input validation absent; stack traces leaked in error responses | **Fixed** — input sanitised; error responses scrubbed |
| 6 | `random_integer` range pinned to a single value (no uniqueness) | **Fixed** — range restored with lifecycle guard |

---

### Breaking Changes

> Operators upgrading from `origin/main` (`baf420f5`) must action all items below before deploying.

#### New Required Secrets / Environment Variables
| Name | Where | Purpose |
|------|-------|---------|
| `AZURE_CLIENT_ID` | GitHub Actions / OIDC | Replaces `AZURE_CREDENTIALS` JSON secret |
| `AZURE_TENANT_ID` | GitHub Actions / OIDC | Required for OIDC federation |
| `AZURE_SUBSCRIPTION_ID` | GitHub Actions / OIDC | Required for OIDC federation |
| `FUNCTION_KEY` | GitHub Actions | HTTP key to call the runner broker Function App |
| `CUSTOM_IMAGE_ID` | GitHub Actions (optional) | Pre-baked Azure VM image for runner VMs |
| `VM_USE_SPOT` | GitHub Actions (optional) | Set `true` to request spot pricing |
| `KEYVAULT_NAME` | Terraform / Function App env | Key Vault holding the runner PAT |

#### Workflow Changes
- `test-build.yml` is the canonical build-test workflow; the old `test-packer-build-win11-on-macos.yml` has been removed — update any branch protection rules that referenced it.
- Self-hosted runner labels have changed; update any workflow `runs-on:` references accordingly.

#### Terraform Prerequisites
- **Terragrunt** must be installed (`terragrunt >= 0.55`).
- Backend configuration files (`backend_azure.hcl` / `backend_local.hcl`) must be populated before `terraform init`.
- The new `azure_gh_runner` module creates an Azure App Registration — ensure the deploying principal has `Application.ReadWrite.OwnedBy` or equivalent AAD permission.

#### ISO Path Change
- Windows 11 ISO is now cached under `./cache/windows11/iso/` (was `vendor/windows/`).
- Update any local scripts or documentation that reference the old path.

#### Hyper-V Windows Provisioning Inventory Removal (Migration Required)
- `inventory/hyperv_windows_winrm.yml` is no longer created or updated by the project.
- Old workflow: scripts/runbooks that invoked `ansible-playbook` with `-i inventory/hyperv_windows_winrm.yml` must be updated.
- New workflow: run the provisioning wrapper from repo root:
  - `go run cmd/main.go provision windows11 --arch amd64 --check`
  - `go run cmd/main.go provision windows11 --arch amd64`
- Required credentials are now read from environment (or project-root `.env`):
  - `HYPERV_WINDOWS_ANSIBLE_USER`
  - `HYPERV_WINDOWS_ANSIBLE_PASSWORD`
- Optional connection overrides:
  - `HYPERV_WINDOWS_ANSIBLE_CONNECTION`
  - `HYPERV_WINDOWS_ANSIBLE_WINRM_TRANSPORT`
  - `HYPERV_WINDOWS_ANSIBLE_PORT`

---

### Dependencies

- Python: `playwright`, `playwright-stealth` (updated version) added to Function App and dependency reconciliation scripts.
- Go: no new direct dependencies; vendor directory kept in sync.
- Terraform providers: `hashicorp/azurerm`, `hashicorp/random` managed by Terragrunt root.
- Packer plugins: managed by `packer init`; ensure network access to the Packer plugin registry on first run.

> **Recommendation:** Add Dependabot configuration for GitHub Actions, Go modules, and Python `requirements.txt` to automate future dependency updates.
