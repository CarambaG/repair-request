CREATE TABLE IF NOT EXISTS services (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(160) NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO services (code, name, sort_order)
VALUES
    ('replace_light_bulb', 'Заменить лампочку', 10),
    ('paint_wall', 'Покрасить стену', 20),
    ('fix_outlet', 'Починить розетку', 30),
    ('install_switch', 'Установить выключатель', 40),
    ('repair_door_lock', 'Починить дверной замок', 50),
    ('fix_plumbing_leak', 'Устранить протечку', 60),
    ('replace_faucet', 'Заменить смеситель', 70),
    ('hang_shelf', 'Повесить полку', 80)
ON CONFLICT (code) DO NOTHING;
