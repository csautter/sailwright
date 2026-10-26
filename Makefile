GO_MOD_TOOLCHAIN ?= go$(shell awk '/^go /{print $$2}' go.mod)
GOSEC ?= $(shell command -v gosec 2>/dev/null || echo "$$(go env GOPATH)/bin/gosec")
BINARY_NAME ?= sailwright
DIST_DIR ?= dist
MAIN_PACKAGE ?= ./cmd/main.go
GATEWAY_BINARY_NAME ?= sailwright-gateway
GATEWAY_PACKAGE ?= ./cmd/sailwright-gateway
VERSION ?= dev
GO_LDFLAGS ?= -s -w
RELEASE_GOOS ?= darwin linux windows
RELEASE_GOARCH ?= amd64 arm64

PACKAGE_VERSION = $(patsubst v%,%,$(VERSION))
TARGET_BINARY_NAME = $(BINARY_NAME)$(if $(filter windows,$(GOOS)),.exe,)
TARGET_DIST_DIR = $(DIST_DIR)/$(GOOS)-$(GOARCH)
# The Homebrew formulae (scripts/homebrew/render-formula.sh) depend on this naming.
RELEASE_ASSET_BASENAME = sailwright_$(PACKAGE_VERSION)_$(GOOS)_$(GOARCH)
TARGET_GATEWAY_BINARY_NAME = $(GATEWAY_BINARY_NAME)$(if $(filter windows,$(GOOS)),.exe,)
GATEWAY_RELEASE_ASSET_BASENAME = $(GATEWAY_BINARY_NAME)_$(PACKAGE_VERSION)_$(GOOS)_$(GOARCH)

.PHONY: build build-cli-local build-gateway-local test-e2e-smoke test-e2e build-cli-target build-cli-release package-cli-target package-cli-release \
	build-gateway-target package-gateway-target clean-dist \
	test-build-runner test-build test-deploy test-provision test-deploy-windows-hyperv test-clean-testcache \
	test-build-integration test-build-specific test-macos-tart-runner test-gh-runner-func-request test-gh-runner-func-delete \
	deploy-plan-terraform-azure-gh-runner deploy-apply-terraform-azure-gh-runner deploy-az-func-app gosec quickstart demo demo-gif demo-build-video

# Quick-start automation: run the full install->build->create->start->provision
# lifecycle for one target and record it (terminal + VM display).
# Override with OS=, TYPE=, ARCH=, or pass extra flags via QUICKSTART_ARGS,
# e.g. `make quickstart TYPE=desktop QUICKSTART_ARGS="--keep"`.
OS ?= ubuntu
TYPE ?= server
ARCH ?= amd64
QUICKSTART_ARGS ?=

build:
	go build ./...

build-cli-local:
	$(MAKE) build-cli-target GOOS=$(shell go env GOOS) GOARCH=$(shell go env GOARCH)

# Host-side MCP gateway for agents in devcontainers (see docs/agent-gateway.md).
build-gateway-local:
	$(MAKE) build-gateway-target GOOS=$(shell go env GOOS) GOARCH=$(shell go env GOARCH)

build-gateway-target:
	@test -n "$(GOOS)" || (echo "GOOS is required" && exit 1)
	@test -n "$(GOARCH)" || (echo "GOARCH is required" && exit 1)
	mkdir -p "$(TARGET_DIST_DIR)"
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags '$(GO_LDFLAGS) -X main.version=$(VERSION)' -o "$(TARGET_DIST_DIR)/$(TARGET_GATEWAY_BINARY_NAME)" $(GATEWAY_PACKAGE)

build-cli-target:
	@test -n "$(GOOS)" || (echo "GOOS is required" && exit 1)
	@test -n "$(GOARCH)" || (echo "GOARCH is required" && exit 1)
	mkdir -p "$(TARGET_DIST_DIR)"
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags '$(GO_LDFLAGS)' -o "$(TARGET_DIST_DIR)/$(TARGET_BINARY_NAME)" $(MAIN_PACKAGE)

build-cli-release: clean-dist
	@set -e; \
	for goos in $(RELEASE_GOOS); do \
		for goarch in $(RELEASE_GOARCH); do \
			$(MAKE) build-cli-target GOOS=$$goos GOARCH=$$goarch VERSION=$(VERSION); \
		done; \
	done

package-cli-target: build-cli-target
	@test -n "$(GOOS)" || (echo "GOOS is required" && exit 1)
	@test -n "$(GOARCH)" || (echo "GOARCH is required" && exit 1)
	@test -n "$(VERSION)" || (echo "VERSION is required" && exit 1)
	rm -f "$(DIST_DIR)/$(RELEASE_ASSET_BASENAME).tar.gz" "$(DIST_DIR)/$(RELEASE_ASSET_BASENAME).zip"
	if [ "$(GOOS)" = "windows" ]; then \
		cd "$(TARGET_DIST_DIR)" && zip -q -j "../$(RELEASE_ASSET_BASENAME).zip" "$(TARGET_BINARY_NAME)"; \
	else \
		tar -C "$(TARGET_DIST_DIR)" -czf "$(DIST_DIR)/$(RELEASE_ASSET_BASENAME).tar.gz" "$(TARGET_BINARY_NAME)"; \
	fi

package-cli-release: clean-dist
	@set -e; \
	for goos in $(RELEASE_GOOS); do \
		for goarch in $(RELEASE_GOARCH); do \
			$(MAKE) package-cli-target package-gateway-target GOOS=$$goos GOARCH=$$goarch VERSION=$(VERSION); \
		done; \
	done

package-gateway-target: build-gateway-target
	@test -n "$(VERSION)" || (echo "VERSION is required" && exit 1)
	rm -f "$(DIST_DIR)/$(GATEWAY_RELEASE_ASSET_BASENAME).tar.gz" "$(DIST_DIR)/$(GATEWAY_RELEASE_ASSET_BASENAME).zip"
	if [ "$(GOOS)" = "windows" ]; then \
		cd "$(TARGET_DIST_DIR)" && zip -q -j "../$(GATEWAY_RELEASE_ASSET_BASENAME).zip" "$(TARGET_GATEWAY_BINARY_NAME)"; \
	else \
		tar -C "$(TARGET_DIST_DIR)" -czf "$(DIST_DIR)/$(GATEWAY_RELEASE_ASSET_BASENAME).tar.gz" "$(TARGET_GATEWAY_BINARY_NAME)"; \
	fi

clean-dist:
	rm -rf "$(DIST_DIR)"

test-build-runner:
	go test ./cmd/cmd/... -run "TestParallelBuilds|TestSequentialBuilds" -v -timeout 60s

test-build:
	# sudo is required to generate a customized Windows 11 iso
	cd ./pkg/build && go test -parallel 4 -timeout 300m -v .

test-deploy:
	go test -timeout 20m -v ./pkg/deploy/...

test-provision:
	go test -timeout 20m -v ./pkg/provision/...

test-deploy-windows-hyperv:
	RUN_INTEGRATION_TESTS=1 RUN_WINDOWS_HYPERV_DEPLOY_SMOKE=1 go test -timeout 60m -v ./pkg/deploy -run "TestGetHypervWindowsBoxPath_UsesExpectedBuildArtifact|TestGetHypervWindowsBoxPath_FallsBackToCachePath|TestRunHypervVagrantDeployOnWindows_Smoke"

# Deterministic E2E scenarios (test/e2e). test-e2e-smoke needs no hypervisor.
# Run other scenarios with E2E_RUN='TestScenarios/vm-lifecycle' E2E_TARGET=ubuntu/server/amd64.
E2E_RUN ?= TestScenarios/smoke
E2E_TARGET ?=
test-e2e-smoke: build-cli-local
	SAILWRIGHT_E2E_BIN="$(abspath $(DIST_DIR))/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)$(shell go env GOEXE)" \
		go test -tags e2e ./test/e2e -run 'TestScenarios/smoke' -count=1 -v

test-e2e: build-cli-local
	SAILWRIGHT_E2E_BIN="$(abspath $(DIST_DIR))/$(shell go env GOOS)-$(shell go env GOARCH)/$(BINARY_NAME)$(shell go env GOEXE)" \
		SAILWRIGHT_E2E_TARGET="$(E2E_TARGET)" go test -tags e2e ./test/e2e -run '$(E2E_RUN)' -count=1 -timeout 6h -v

test-clean-testcache:
	go clean -testcache

test-build-integration:
	cd ./pkg/build && RUN_INTEGRATION_TESTS=1 go test -timeout 300m -v . -run "TestIntegrationDependencyReconciliation|TestGetWindows11DownloadAmd64|TestGetWindows11DownloadArm64|TestResolveDebianPackageURL|TestResolveAndDownloadQemuEfiAarch64"

test-build-specific:
	# Usage: make test-build-specific TEST_NAME=<name of the test>
	cd ./pkg/build && go test -timeout 300m -v . -run $(TEST_NAME)

test-macos-tart-runner:
	bash ./.github/runners/test-create-macos-tart-runner.sh

test-gh-runner-func-request:
	# Usage:
	# make test-gh-runner-func-request FUNCTION_APP_NAME=<function-app-name>
	# Example:
	# make test-gh-runner-func-request FUNCTION_APP_NAME=my-func-app
	# Optional:
	# API_CLIENT_ID=<api-client-id> TENANT_ID=<tenant-id>
	# REPO=<owner/repo> RESOURCE_GROUP=<name> RUNNER_NAME=<name> VIRTUALIZATION_FLAVOR=hyperv|virtualbox
	# AZURE_BROKER_CONFIG_DIR=~/.azure-broker AZURE_ADMIN_CONFIG_DIR=~/.azure
	# TERRAGRUNT_OUTPUT_DIR=deployments/terraform/env/azure_dev/azure_gh_runner
	FUNCTION_APP_NAME="$(FUNCTION_APP_NAME)" \
	API_CLIENT_ID="$(API_CLIENT_ID)" \
	TENANT_ID="$(TENANT_ID)" \
	REPO="$(REPO)" \
	RESOURCE_GROUP="$(RESOURCE_GROUP)" \
	RUNNER_NAME="$(RUNNER_NAME)" \
	VIRTUALIZATION_FLAVOR="$(VIRTUALIZATION_FLAVOR)" \
	AZURE_BROKER_CONFIG_DIR="$(AZURE_BROKER_CONFIG_DIR)" \
	AZURE_ADMIN_CONFIG_DIR="$(AZURE_ADMIN_CONFIG_DIR)" \
	TERRAGRUNT_OUTPUT_DIR="$(TERRAGRUNT_OUTPUT_DIR)" \
	bash ./scripts/gh-runner-func/test-endpoints.sh --request-runner

test-gh-runner-func-delete:
	# Usage:
	# make test-gh-runner-func-delete FUNCTION_APP_NAME=<function-app-name> RESOURCE_GROUP=<name>
	# Optional: API_CLIENT_ID=<api-client-id> TENANT_ID=<tenant-id> RUNNER_NAME=<name>
	FUNCTION_APP_NAME="$(FUNCTION_APP_NAME)" \
	API_CLIENT_ID="$(API_CLIENT_ID)" \
	TENANT_ID="$(TENANT_ID)" \
	RESOURCE_GROUP="$(RESOURCE_GROUP)" \
	RUNNER_NAME="$(RUNNER_NAME)" \
	AZURE_BROKER_CONFIG_DIR="$(AZURE_BROKER_CONFIG_DIR)" \
	AZURE_ADMIN_CONFIG_DIR="$(AZURE_ADMIN_CONFIG_DIR)" \
	TERRAGRUNT_OUTPUT_DIR="$(TERRAGRUNT_OUTPUT_DIR)" \
	bash ./scripts/gh-runner-func/test-endpoints.sh --delete-resource-group

deploy-plan-terraform-azure-gh-runner:
	cd deployments/terraform/env/azure_dev/azure_gh_runner && terragrunt plan -out tf.plan

deploy-apply-terraform-azure-gh-runner:
	cd deployments/terraform/env/azure_dev/azure_gh_runner && terragrunt apply tf.plan

deploy-az-func-app:
	# Usage:
	# make deploy-az-func-app FUNCTION_APP_NAME=<function-app-name>
	cd ./scripts/gh-runner-func/ && \
	func azure functionapp publish $(FUNCTION_APP_NAME)

quickstart:
	bash ./scripts/quickstart.sh --os $(OS) --type $(TYPE) --arch $(ARCH) $(QUICKSTART_ARGS)

# Regenerate the website terminal demo cast from demo/quickstart.demo.
demo:
	python3 demo/generate_cast.py --output demo/quickstart.cast

# Render the demo cast to demo/quickstart.gif (for the README) via agg.
demo-gif:
	bash demo/render-gif.sh

# Turn a raw VM-build recording (artifact/build-<slug>.vnc.mp4) into
# demo/vm-build.mp4 + demo/vm-build.gif. Pass extra flags via DEMO_BUILD_ARGS,
# e.g. `make demo-build-video DEMO_BUILD_ARGS="--speed 16 --start 5"`.
DEMO_BUILD_ARGS ?=
demo-build-video:
	bash demo/process-build-video.sh $(DEMO_BUILD_ARGS)

# Run Go security scanning via gosec.
# If gosec reports type errors due to Go toolchain export data version mismatches
# (e.g. devcontainer toolchain newer than go.mod), match the go.mod toolchain:
#   GOTOOLCHAIN=go1.26.0 make gosec
gosec:
	@if [ ! -x "$(GOSEC)" ]; then \
		echo "gosec not found at $(GOSEC). Rebuild the dev container or run: go install github.com/securego/gosec/v2/cmd/gosec@v2.30.0"; \
		exit 1; \
	fi
	@# -no-fail exits 0 even when gosec cannot type-check packages (e.g. a gosec built against an
	@# x/tools too old for the Go toolchain), which would report "no issues" without analyzing anything.
	@$(GOSEC) -no-fail -fmt sarif -out gosec-results.sarif -exclude-generated ./... 2>gosec-stderr.log; status=$$?; \
	if [ $$status -ne 0 ] || grep -q 'type errors' gosec-stderr.log; then \
		grep -E 'type errors|[Ee]rror' gosec-stderr.log | head -5; \
		rm -f gosec-stderr.log; \
		echo "gosec did not analyze all packages; the results above are incomplete."; \
		echo "Note: If the container Go version is newer than go.mod, run with the go.mod toolchain:"; \
		echo "      GOTOOLCHAIN=$(GO_MOD_TOOLCHAIN) make gosec"; \
		exit 1; \
	fi; \
	rm -f gosec-stderr.log
