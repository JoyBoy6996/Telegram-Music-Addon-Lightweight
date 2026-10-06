FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY telegram-music-addon /app/telegram-music-addon
COPY icon.png /app/icon.png

ENV GOMEMLIMIT=90MiB
ENV GOGC=50
ENV PORT=3000

EXPOSE 3000

USER nonroot:nonroot

ENTRYPOINT ["/app/telegram-music-addon"]