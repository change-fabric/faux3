# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/faux3 ./cmd/faux3
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/faux3 /faux3
COPY --from=build --chown=65532:65532 /out/data /data
ENV FAUX3_DATA_DIR=/data FAUX3_PORT=9000
VOLUME /data
EXPOSE 9000
USER nonroot:nonroot
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 CMD ["/faux3", "healthcheck"]
ENTRYPOINT ["/faux3"]
