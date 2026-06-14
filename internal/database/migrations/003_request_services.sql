CREATE TABLE IF NOT EXISTS request_services (
    id BIGSERIAL PRIMARY KEY,
    request_id BIGINT NOT NULL REFERENCES repair_requests(id) ON DELETE CASCADE,
    service_code VARCHAR(64) NOT NULL,
    service_name VARCHAR(160) NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    UNIQUE (request_id, service_code)
);

CREATE INDEX IF NOT EXISTS idx_request_services_request_id
    ON request_services(request_id);
