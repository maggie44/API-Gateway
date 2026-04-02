FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# This shared build stage intentionally compiles all local binaries for simplicity in
# this repository. A production container pipeline would usually split these
# into per-target build stages or separate image builds so each artefact only pays for
# the binary it actually needs.
RUN go build -o /bin/gateway ./cmd/gateway && \
    go build -o /bin/mock-backend ./cmd/mock-backend && \
    go build -o /bin/token-seed ./cmd/token-seed


FROM gcr.io/distroless/base-debian13:nonroot AS gateway

WORKDIR /app

COPY --from=build /bin/gateway /usr/local/bin/gateway

ENTRYPOINT ["/usr/local/bin/gateway"]


FROM gcr.io/distroless/base-debian13:nonroot AS mock-backend

WORKDIR /app

COPY --from=build /bin/mock-backend /usr/local/bin/mock-backend

ENTRYPOINT ["/usr/local/bin/mock-backend"]
