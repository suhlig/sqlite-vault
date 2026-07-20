# syntax=docker/dockerfile:1

FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -o /bin/sqlite-vault-verify ./cmd/sqlite-vault-verify

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /bin/sqlite-vault-verify /sqlite-vault-verify

ENTRYPOINT ["/sqlite-vault-verify"]
