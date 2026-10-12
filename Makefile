# game: an endless runner in the terminal, played by you and by a local decision
# model at the same time, on the same map.
#
# Override any variable on the command line, e.g.:
#   make run SPEED=30 DIFFICULTY=hard
#   make sim SIM=30s PROCESSOR=cpu
#   make table MODEL='https://huggingface.co/ggml-org/Clef-Flash-GGUF/resolve/main/Clef-Flash-Q4_K_M.gguf'

# ----- Configuration --------------------------------------------------------

# Decision model to download and run: the URL of a GGUF file on Hugging Face.
MODEL     ?= https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF/resolve/main/Jev-Style-0.8B-Decision-v3-Q8_0.gguf
# Map scrolling speed at the start, in cells per second (5 to 60).
SPEED     ?= 20
# Map seed, the same map for the same seed. Random when empty.
SEED      ?=
# Difficulty at the start: easy | medium | hard. d changes it in the game.
DIFFICULTY ?= easy
# How long `make sim` lets the model play.
SIM       ?= 60s

# Which llama.cpp build to download and run: cuda | vulkan | metal | cpu. When
# unset: metal on an Apple Silicon Mac (an Intel Mac only has cpu), cuda if
# nvidia-smi is found, vulkan on an AMD GPU, if a Vulkan GPU driver is installed
# or if vulkaninfo is found, else cpu. lvp_icd is Mesa's Vulkan on the CPU,
# slower than the cpu build, so it doesn't count as a driver.
PROCESSOR ?= $(shell \
	if [ "$$(uname -s)" = Darwin ]; then \
		if [ "$$(uname -m)" = arm64 ]; then echo metal; else echo cpu; fi; \
	elif command -v nvidia-smi >/dev/null 2>&1; then echo cuda; \
	elif command -v rocminfo >/dev/null 2>&1 || [ -e /dev/kfd ]; then echo vulkan; \
	elif ls /usr/share/vulkan/icd.d/*.json /usr/local/share/vulkan/icd.d/*.json /etc/vulkan/icd.d/*.json 2>/dev/null \
		| grep -qv lvp_icd; then echo vulkan; \
	elif command -v vulkaninfo >/dev/null 2>&1; then echo vulkan; \
	else echo cpu; fi)

# Environment and flags shared by the targets that load the model.
RUN_ENV  = MODEL='$(MODEL)' KRONK_PROCESSOR=$(PROCESSOR)
RUN_ARGS = -speed $(SPEED) -difficulty $(DIFFICULTY) $(if $(SEED),-seed $(SEED))

.DEFAULT_GOAL := help

# ----- Run ------------------------------------------------------------------

.PHONY: run
run: ## Play against the model, then print a recap
	@$(RUN_ENV) go run . $(RUN_ARGS)

.PHONY: sim
sim: ## Let the model play alone for SIM (default 60s), then print a recap
	@$(RUN_ENV) go run . -sim $(SIM) $(RUN_ARGS)

.PHONY: table
table: ## Ask the model about each obstacle at each distance, print its accuracy
	@$(RUN_ENV) go run . -table

# ----- Docker ---------------------------------------------------------------

IMAGE ?= kronk-jev-game
# Docker can't reach Metal, so on a Mac the image runs on the cpu. The image is
# tagged with its processor: docker-run runs the one built for this machine.
DOCKER_PROCESSOR ?= $(if $(filter metal,$(PROCESSOR)),cpu,$(PROCESSOR))
RUNTIME_IMAGE    ?= $(if $(filter cuda,$(DOCKER_PROCESSOR)),nvidia/cuda:13.0.1-runtime-ubuntu24.04,ubuntu:24.04)

# The GPU device nodes belong to the host's video and render groups. Their ids
# usually differ in the container, so pass them by number.
VIDEO_GID  := $(shell getent group video 2>/dev/null | cut -d: -f3)
RENDER_GID := $(shell getent group render 2>/dev/null | cut -d: -f3)
DRI_FLAGS   = --device /dev/dri $(if $(VIDEO_GID),--group-add $(VIDEO_GID)) $(if $(RENDER_GID),--group-add $(RENDER_GID))
GPU_FLAGS   = $(if $(filter vulkan,$(DOCKER_PROCESSOR)),$(DRI_FLAGS))$(if $(filter cuda,$(DOCKER_PROCESSOR)),--gpus all)

.PHONY: docker-build
docker-build: ## Build the Docker image, with llama.cpp and the model in it
	docker build \
		--build-arg MODEL='$(MODEL)' \
		--build-arg PROCESSOR=$(DOCKER_PROCESSOR) \
		--build-arg RUNTIME_IMAGE=$(RUNTIME_IMAGE) \
		-t $(IMAGE):$(DOCKER_PROCESSOR) .

.PHONY: docker-run
docker-run: ## Play against the model in Docker
	@docker run -it --rm $(GPU_FLAGS) -e TERM -e COLORTERM $(IMAGE):$(DOCKER_PROCESSOR) $(RUN_ARGS)

# ----- Develop --------------------------------------------------------------

.PHONY: build
build: ## Build the binary
	go build -o kronk-jev-game .

.PHONY: test
test: ## Run the unit tests (no model needed)
	go test ./...

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Config: MODEL=$(MODEL) PROCESSOR=$(PROCESSOR) SPEED=$(SPEED) DIFFICULTY=$(DIFFICULTY) SIM=$(SIM)"
