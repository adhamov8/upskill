# --- builder ---
FROM golang:1.23 as builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go mod tidy

RUN CGO_ENABLED=0 go build -tags timetzdata -o /out/app ./cmd/app

# --- runner ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
ENV TZ=UTC
COPY --from=builder /out/app /app/app
EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/app/app"]
