FROM golang:1.26-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates
# Athens serves private kits with public proxy fallback.
ARG GOPROXY=http://athens.infra.svc.cluster.local:3000,https://proxy.golang.org
ARG GONOSUMDB=github.com/Defolt-Labs/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/defolt-tenants-service .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/defolt-tenants-service /app/defolt-tenants-service
EXPOSE 8080
ENTRYPOINT ["/app/defolt-tenants-service"]
