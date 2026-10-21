# TARGET picks the entrypoint under cmd/, flagship or escort
ARG TARGET=flagship

FROM golang:1.27-alpine AS build
ARG TARGET
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/haven ./cmd/${TARGET}

FROM alpine:3.22
# ca-certificates is needed for the MaxMind download
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/haven /app/haven

# The app only shuts down on SIGINT
STOPSIGNAL SIGINT
ENTRYPOINT ["/app/haven"]
