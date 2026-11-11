FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /temperature-api .

FROM alpine:3.20
COPY --from=builder /temperature-api /temperature-api
USER nobody
EXPOSE 8081
CMD ["/temperature-api"]
