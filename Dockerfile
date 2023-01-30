FROM golang:latest

RUN mkdir /tgbot
ADD . /tgbot/
WORKDIR /tgbot
RUN go build -o main .
ENTRYPOINT ["/tgbot/main"]