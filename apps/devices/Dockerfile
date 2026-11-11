FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /devices .

FROM alpine:3.20
COPY --from=builder /devices /devices
USER nobody
EXPOSE 8082
CMD ["/devices"]
