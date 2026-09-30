FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -o /hapsis ./cmd/hapsis

FROM gcr.io/distroless/static-debian12
COPY --from=build /hapsis /hapsis
ENTRYPOINT ["/hapsis"]
