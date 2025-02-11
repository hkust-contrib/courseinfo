FROM golang:1.22 AS builder
WORKDIR /app
ENV GOOS=linux
ENV GOARCH=amd64
ENV CGO_ENABLED=0
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /app/courseinfo

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=builder /app/courseinfo /app/courseinfo
EXPOSE 8080
EXPOSE 2112
ENTRYPOINT ["/app/courseinfo", "-precache"]
