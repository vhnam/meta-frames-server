FROM golang:1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags=sonic -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
# The base image already defaults to nonroot; stating it lets scanners (Trivy DS-0002) see it.
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
