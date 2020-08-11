FROM "node:14"

WORKDIR /usr/src/app

COPY package*.json ./
RUN npm ci --only=production

COPY dist dist
COPY server.js server.js

EXPOSE 3000

CMD [ "node", "server.js" ]
