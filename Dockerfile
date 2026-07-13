# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/defolt-tenants-service ./

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/defolt-tenants-service /app/defolt-tenants-service
EXPOSE 8080
ENTRYPOINT ["/app/defolt-tenants-service"]
