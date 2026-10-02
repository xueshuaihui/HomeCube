# =============================================================================
# HomeCube · web H5 static container Dockerfile
#
# Serves the built H5 static assets using nginx:alpine.
# The build output from `npm run build:h5` is copied to nginx's document root.
# =============================================================================

FROM nginx:1.27-alpine

# Remove default nginx configuration
RUN rm /etc/nginx/conf.d/default.conf

# Copy custom nginx config for static serving
COPY nginx-web.conf /etc/nginx/conf.d/default.conf

# Copy built H5 assets to nginx document root
# Build first with: cd web && npm run build:h5
COPY dist/build/h5 /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 \
    CMD wget -q --spider http://127.0.0.1/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
