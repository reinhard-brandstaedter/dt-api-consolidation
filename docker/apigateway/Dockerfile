FROM golang:1.15.0-buster

RUN apt-get update -y
RUN apt-get install net-tools -y
RUN go get github.com/gomodule/redigo/redis
RUN go get golang.org/x/net/http2

RUN mkdir /go/src/dtapigw
WORKDIR /go/src/dtapigw
COPY dtapigw .
 
RUN go build -o dtapigw

CMD ["./dtapigw"]