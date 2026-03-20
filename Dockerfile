FROM node:20-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist /app/cmd/et/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /et ./cmd/et

FROM alpine:3.21
RUN apk add --no-cache bash
COPY --from=build /et /usr/local/bin/et
EXPOSE 2840
ENTRYPOINT ["et"]
CMD ["server"]
