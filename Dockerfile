FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
    -ldflags "-w -s -extldflags '-static' -X 'main.Version=${VERSION}'" \
    -o profile-stats ./cmd/server
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/profile-stats /profile-stats
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/profile-stats"]
