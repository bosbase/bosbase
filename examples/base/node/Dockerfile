FROM node:20-alpine AS deps

WORKDIR /app/node

COPY package*.json ./
COPY tsconfig.json ./
RUN npm ci

FROM node:20-alpine AS runner

ENV NODE_ENV=production
WORKDIR /app/node

COPY --from=deps /app/node/node_modules ./node_modules
COPY . .

CMD ["npm", "run", "sync"]
