FROM golang:1.24.4-alpine3.21 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod tidy
COPY . ./

#RUN CGO_ENABLED=0 GOOS=linux go build -o ushort main.go
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-extldflags "-static"' -o watcher ./cmd/main.go


FROM scratch
COPY --from=builder /app/watcher /app/
WORKDIR /app
CMD ["/app/watcher"]