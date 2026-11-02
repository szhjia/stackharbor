# Local Linux ARM64 verification without altering host toolchains or packages.
FROM golang:1.26-bookworm
ARG NODE_VERSION=26.9.0
ENV DEBIAN_FRONTEND=noninteractive CARGO_HOME=/opt/cargo RUSTUP_HOME=/opt/rustup
ENV PATH=/opt/cargo/bin:/usr/local/go/bin:/usr/local/sbin:/usr/sbin:/sbin:/usr/local/bin:/usr/bin:/bin
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl xz-utils build-essential pkg-config libssl-dev libwebkit2gtk-4.1-dev libappindicator3-dev librsvg2-dev patchelf xdg-utils lsof procps xvfb xauth dbus-x11 webkit2gtk-driver python3 git && rm -rf /var/lib/apt/lists/*
RUN arch="$(dpkg --print-architecture)" && case "$arch" in arm64) node_arch=arm64;; amd64) node_arch=x64;; *) exit 1;; esac && curl -fsSLO "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" && curl -fsSLO "https://nodejs.org/dist/v${NODE_VERSION}/SHASUMS256.txt" && awk -v file="node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" '$2 == file' SHASUMS256.txt | sha256sum -c - && tar -xJf "node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" --strip-components=1 -C /usr/local && rm node-v*.tar.xz SHASUMS256.txt
RUN curl -fsSL https://sh.rustup.rs -o /tmp/rustup.sh && sh /tmp/rustup.sh -y --profile minimal --default-toolchain 1.95.0 --component rustfmt --component clippy && rm /tmp/rustup.sh
WORKDIR /work
