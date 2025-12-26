FROM python:3.12-slim

# Install uv.
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

WORKDIR /app

# Copy only dependency files first to leverage Docker layer cache.
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen

# Copy the rest of the application.
COPY . .

RUN chmod +x start.sh

EXPOSE 5677
CMD ["/app/start.sh"]
