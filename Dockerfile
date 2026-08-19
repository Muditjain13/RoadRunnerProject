# Single Dockerfile for every service in this repo; SERVICE selects which
# cmd/<SERVICE> binary gets built. One Dockerfile instead of eight
# near-identical copies - see deploy/compose/docker-compose.yml, where
# each service passes its own SERVICE build arg.
ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-bookworm AS build
ARG SERVICE
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/service /service
ENTRYPOINT ["/service"]
