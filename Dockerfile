FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY . .
RUN go build -o node ./cmd/node/

FROM alpine:3.19

WORKDIR /app
COPY --from=builder /app/node .

EXPOSE 3000 8080

ENTRYPOINT ["./node"]
