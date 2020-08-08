/* eslint-env node */
const fs = require('fs');
const path = require('path');
const fastify = require('fastify')({ logger: true });

// Build an env object from the local environment
const env = {
  host: process.env.NOMAD_API,
};

const redirect = async (request, reply) => {
  reply.redirect('/ui/');
};

// Generate the index html file one time
let index = fs.readFileSync('dist/index.html', 'utf8');
index = index.replace('[[buildenv]]', encodeURI(JSON.stringify(env)));

// Static assets
fastify.register(require('fastify-static'), {
  root: path.join(__dirname, 'dist/assets'),
  prefix: '/ui/assets/',
});

// index file (with client-side redirect support)
fastify.get('/ui/*', async (request, reply) => {
  reply.type('text/html').send(index);
});

fastify.get('/', redirect);
fastify.get('/ui', redirect);

const start = async () => {
  try {
    await fastify.listen(3000);
    fastify.log.info(`server listening on ${fastify.server.address().port}`);
  } catch (err) {
    fastify.log.error(err);
    process.exit(1);
  }
};

start();
