# =============================================================================
# HomeCube · admin static container Dockerfile
#
# Serves the built admin static assets using nginx:alpine.
# The build output is copied to nginx's document root.
# =============================================================================

FROM nginx:1.27-alpine

# Remove default nginx configuration
RUN rm /etc/nginx/conf.d/default.conf

# Copy custom nginx config for static serving
COPY deploy/nginx-admin.conf /etc/nginx/conf.d/default.conf

# Copy built admin assets to nginx document root
# Build first with: cd admin && npm run build (adjust as needed)
COPY dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 \
    CMD wget -q --spider http://127.0.0.1/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
