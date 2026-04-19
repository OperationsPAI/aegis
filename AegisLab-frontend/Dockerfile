FROM node:20-alpine AS builder

# derive package manager name and version from package.json using jq
RUN pm=$(jq -r '.packageManager|split("@")[0]' package.json 2>/dev/null || echo pnpm) && \
    ver=$(jq -r '.packageManager|split("@")[1]' package.json 2>/dev/null || echo latest) && \
    npm install -g "$pm@$ver"

WORKDIR /app

COPY package.json pnpm-lock.yaml .npmrc ./

RUN --mount=type=secret,id=NPM_TOKEN \
    export NPM_TOKEN=$(cat /run/secrets/NPM_TOKEN) && \
    pnpm install --no-frozen-lockfile

COPY . .

RUN pnpm run build

FROM nginx:stable-alpine

COPY --from=builder /app/dist /usr/share/nginx/html

COPY nginx.conf /etc/nginx/templates/default.conf.template

EXPOSE 80