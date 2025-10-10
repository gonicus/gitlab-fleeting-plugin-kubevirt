FROM golang:1.24-alpine AS builder

WORKDIR /opt

COPY . .
RUN go build -v ./cmd/fleeting-plugin-kubevirt

FROM alpine:3.5

# We'll likely need to add SSL root certificates
RUN apk --no-cache add ca-certificates

WORKDIR /usr/local/bin

COPY --from=builder /opt/fleeting-plugin-kubevirt .
CMD ["./fleeting-plugin-kubevirt"]
