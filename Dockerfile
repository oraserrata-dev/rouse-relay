FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o rouse-relay .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/rouse-relay /usr/local/bin/rouse-relay
EXPOSE 9876
ENTRYPOINT ["rouse-relay"]
