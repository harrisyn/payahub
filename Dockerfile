FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /payahub ./cmd/payahub

# Tesseract and pdftoppm read claim forms on this server, to black out the
# patient's identity before a model sees them.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-eng poppler-utils ca-certificates \
    && rm -rf /var/lib/apt/lists/* && useradd --system --uid 65532 nonroot
USER nonroot
COPY --from=build /payahub /payahub
EXPOSE 8080
ENTRYPOINT ["/payahub"]
CMD ["api"]
