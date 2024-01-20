FROM golang:1.21 as builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY ./ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/acch

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=builder /app/bin/acch /app/bin/acch
EXPOSE 8080
ENTRYPOINT ["/app/bin/acch"]