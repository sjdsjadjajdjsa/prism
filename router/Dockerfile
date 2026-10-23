FROM node:24-alpine
WORKDIR /app
COPY server.mjs custom.mjs providers.json ./
RUN mkdir /data && chown node:node /data
USER node
ENV STATS_FILE=/data/speed.json
CMD ["node", "server.mjs"]
