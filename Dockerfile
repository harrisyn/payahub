FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /payahub ./cmd/payahub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /payahub /payahub
EXPOSE 8080
ENTRYPOINT ["/payahub"]
CMD ["api"]
