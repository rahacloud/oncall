# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /oncall .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /oncall /usr/local/bin/oncall
ENV ONCALL_ADDR=:8080 \
    ONCALL_SCHEDULE=/data/schedule.yaml
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/oncall", "serve"]
