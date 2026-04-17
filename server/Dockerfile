FROM golang:latest AS build
WORKDIR /app

ARG GITHUB_TOKEN
RUN git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"
ENV GOPRIVATE=github.com/chariot-giving/*

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./server

FROM gcr.io/distroless/static
COPY --from=build /server /server
CMD ["/server"]
