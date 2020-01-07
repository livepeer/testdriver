FROM golang:1.13-alpine as builder

RUN mkdir /testdriver
WORKDIR /testdriver

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .
RUN go build .

FROM alpine:latest

COPY --from=builder /testdriver/testdriver /usr/local/bin/

ENTRYPOINT ["testdriver"]
