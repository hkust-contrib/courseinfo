FROM golang:1.22-alpine AS builder
WORKDIR /app
ENV GOOS=linux
ENV GOARCH=amd64
ENV CGO_ENABLED=0
COPY go.mod ./
RUN apk --no-cache add ca-certificates
RUN go mod download
COPY . .
RUN go build -o /app/courseinfo

FROM scratch

COPY --from=builder /app/courseinfo /app/courseinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8080
EXPOSE 2112
ENTRYPOINT ["/app/courseinfo", "-precache"]
