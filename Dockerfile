FROM openintegration/serivce-airtable:0.0.1 as airtable
FROM openintegration/serivce-trello:0.0.4 as trello
FROM openintegration/serivce-google-calendar:0.0.3 as google_calendar
FROM golang:1.16.4-alpine as build

RUN apk update && apk add make wget git gcc build-base bash

WORKDIR /app

COPY . .

RUN go build -o syncer main.go 

FROM alpine:3.12

COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip

COPY --from=airtable /app/service /usr/local/bin/airtable
ENV AIRTABLE_SERVICE_LOCATION=/usr/local/bin/airtable

COPY --from=trello /app/service /usr/local/bin/trello
ENV TRELLO_SERVICE_LOCATION=/usr/local/bin/trello

COPY --from=google_calendar /app/service /usr/local/bin/google_calendar
ENV GOOGLE_CALENDAR_SERVICE_LOCATION=/usr/local/bin/google_calendar

COPY --from=build /app/syncer /usr/local/bin/syncer

CMD [ "syncer" ]