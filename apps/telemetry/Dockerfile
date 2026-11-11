FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /telemetry .

FROM alpine:3.20
COPY --from=builder /telemetry /telemetry
USER nobody
EXPOSE 8083
CMD ["/telemetry"]
