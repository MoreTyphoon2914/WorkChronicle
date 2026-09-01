FROM golang:1.26@sha256:e30143be198ab04cf7ba25fba83ab3a692ca584c994aad0bf131fa0eb32dd8c1 AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN mkdir -p /out/data && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/workchronicle-core ./cmd/workchronicle-core

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/workchronicle-core /workchronicle-core
COPY --from=build --chown=nonroot:nonroot /out/data /data
USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 CMD ["/workchronicle-core", "healthcheck"]
ENTRYPOINT ["/workchronicle-core"]
