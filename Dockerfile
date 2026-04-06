# Multi-stage build
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/duman-client ./cmd/duman-client
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/duman-relay ./cmd/duman-relay

FROM scratch
# Client image
FROM scratch AS client
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/duman-client /duman-client
ENTRYPOINT ["/duman-client"]

# Relay image
FROM scratch AS relay
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/duman-relay /duman-relay
EXPOSE 5432 3306 443 9091
ENTRYPOINT ["/duman-relay"]
