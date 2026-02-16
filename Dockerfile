FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /out/clawlet ./cmd/clawlet

FROM alpine:latest

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/clawlet /usr/local/bin/clawlet

# Create config directory
RUN mkdir -p /root/.clawlet

EXPOSE 18790

ENTRYPOINT ["clawlet"]
CMD ["status"]
