# One image for the api, ingest, worker and migrate commands.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ecogo ./cmd/ecogo

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ecogo /ecogo
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ecogo"]
CMD ["api"]
