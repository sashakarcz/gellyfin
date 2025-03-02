FROM golang:1.17-alpine

apk add --no-cache nomad

WORKDIR /app

COPY . .

RUN go build -o main .

EXPOSE 8080

CMD ["./main"]
