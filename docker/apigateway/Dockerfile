FROM golang:1.15-alpine

#RUN apt-get update -y
#RUN apt-get install net-tools -y
RUN apk update
RUN apk upgrade
RUN apk add --no-cache git
RUN go get github.com/gomodule/redigo/redis
RUN go get golang.org/x/net/http2

RUN mkdir /go/src/dtapigw
WORKDIR /go/src/dtapigw
COPY dtapigw .
 
RUN go build -o dtapigw

FROM alpine:latest
WORKDIR /root/
COPY --from=0 /go/src/dtapigw/dtapigw ./
CMD ["./dtapigw"]