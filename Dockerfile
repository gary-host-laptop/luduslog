# syntax=docker/dockerfile:1
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/luduslog ./cmd/luduslog

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/luduslog /usr/local/bin/luduslog
COPY --from=build /src/config.example.toml /etc/luduslog/config.toml
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/luduslog"]
CMD ["serve", "-config", "/etc/luduslog/config.toml"]
