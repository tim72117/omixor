# omixor-server:omixor 核心的線上服務(Cloud Run)。在 omixor-core/ 底下建置:
#   docker build -t omixor-server .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /omixor-server ./cmd/omixor-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /omixor-server /omixor-server
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/omixor-server"]
