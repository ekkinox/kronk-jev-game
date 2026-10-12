# syntax=docker/dockerfile:1
#
# A self-contained image: llama.cpp and the model are downloaded during the
# build, so the game starts right away, with no Go and no download.
#
# Run it with a terminal attached: docker run -it --rm kronk-jev-game
# `make docker-build` and `make docker-run` set the right flags for your GPU.

ARG GO_IMAGE=golang:1.27-trixie
# Ubuntu's Mesa is recent enough for new GPUs, like Intel Arc on Arrow Lake.
ARG RUNTIME_IMAGE=ubuntu:24.04
ARG MODEL=https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF/resolve/main/Jev-Style-0.8B-Decision-v3-Q8_0.gguf
# Which llama.cpp build to bake in and run: cpu | vulkan | cuda. It is fixed
# at build time, since its libraries are downloaded into the image. For cuda,
# also pass an nvidia/cuda RUNTIME_IMAGE (the Makefile does).
ARG PROCESSOR=cpu

# ----- Build: compile the game, download llama.cpp and the model ------------

FROM ${GO_IMAGE} AS builder
ARG MODEL
ARG PROCESSOR
ENV HOME=/root
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/kronk-jev-game .

# Into /root/.kronk-game. KRONK_PROCESSOR picks the build for the container's
# processor, not the build host's.
ENV MODEL=${MODEL} KRONK_PROCESSOR=${PROCESSOR}
RUN kronk-jev-game -download

# ----- Run: only the libraries llama.cpp needs -------------------------------

FROM ${RUNTIME_IMAGE}
ARG MODEL
ARG PROCESSOR
RUN apt-get update \
    && apt-get install -y --no-install-recommends libgomp1 libstdc++6 ca-certificates \
    && if [ "$PROCESSOR" = vulkan ]; then \
         apt-get install -y --no-install-recommends libvulkan1 mesa-vulkan-drivers; \
       fi \
    && rm -rf /var/lib/apt/lists/*

ENV HOME=/root
COPY --from=builder /usr/local/bin/kronk-jev-game /usr/local/bin/kronk-jev-game
COPY --from=builder /root/.kronk-game /root/.kronk-game

ENV MODEL=${MODEL} KRONK_PROCESSOR=${PROCESSOR}
ENTRYPOINT ["kronk-jev-game"]
