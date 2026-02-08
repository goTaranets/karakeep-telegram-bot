FROM golang:1.22 AS build

WORKDIR /src

# Allow go commands inside the build to update go.sum as needed.
ENV GOFLAGS=-mod=mod

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/karakeep-telegram-bot ./cmd/bot

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/karakeep-telegram-bot /app/karakeep-telegram-bot

ENV LISTEN_ADDR=0.0.0.0:8080

EXPOSE 8080

ENTRYPOINT ["/app/karakeep-telegram-bot"]

