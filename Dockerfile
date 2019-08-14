FROM alpine
RUN adduser -D -H node-labeler
USER node-labeler
ADD node-labeler /
CMD ["/node-labeler"]
