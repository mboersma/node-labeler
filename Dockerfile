# FROM scratch
FROM alpine
ADD node-labeler /
CMD ["/node-labeler"]
