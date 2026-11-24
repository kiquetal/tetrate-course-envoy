FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/xds-server .

FROM alpine:3.19
COPY --from=build /out/xds-server /usr/local/bin/xds-server
EXPOSE 18000 19000
ENTRYPOINT ["/usr/local/bin/xds-server"]
