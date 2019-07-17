FROM alpine
COPY migration /migration
ENTRYPOINT ["/migration"]