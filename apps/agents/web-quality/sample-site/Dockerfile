FROM python:3.13-alpine
WORKDIR /site
COPY apps/agents/web-quality/sample-site/*.html apps/agents/web-quality/sample-site/*.css ./
USER 1000:1000
EXPOSE 8080
CMD ["python", "-m", "http.server", "8080", "--bind", "0.0.0.0"]
