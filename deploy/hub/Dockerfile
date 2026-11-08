# wdx-hub in a container. Built from the repository's root:
#
#   docker build -f deploy/hub/Dockerfile --build-arg VERSION=<version> --build-arg COMMIT=<commit> .
#
# compose.yaml in this directory builds and runs it; README.md says how.

FROM golang:1.26 AS build
ARG VERSION=0.0.0-dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X github.com/srjn45/wdx/internal/identity.Version=${VERSION} -X github.com/srjn45/wdx/internal/identity.Commit=${COMMIT}" \
	-o /out/wdx-hub ./cmd/wdx-hub
# The static image has no shell to create the home with, so it is made here,
# owned by the image's nonroot user; a new named volume takes its owner.
RUN mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/wdx-hub /usr/local/bin/wdx-hub
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV WDX_HUB_HOME=/data
VOLUME /data
EXPOSE 8080 8443
ENTRYPOINT ["wdx-hub", "serve"]
