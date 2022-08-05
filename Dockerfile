FROM golang:1.18 AS build
WORKDIR /app
COPY go.mod go.sum /app/
RUN go mod download

COPY . .
RUN go build -o dcp_troublemaker

FROM debian:bullseye-slim
COPY --from=build /app/dcp_troublemaker /dcp_troublemaker
ENTRYPOINT ["/dcp_troublemaker"]
