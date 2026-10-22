-- An organisation's own price list: its codes for the services, medicines and
-- supplies it bills, with their names and prices. A claim line that names one
-- of these codes and gives no price takes the price from here. The standard
-- item behind each code stays in item_mappings, confirmed by a person.
CREATE TABLE IF NOT EXISTS price_list (
    organization_id  text NOT NULL REFERENCES organizations (id),
    local_code       text NOT NULL,
    name             text NOT NULL,
    kind             text NOT NULL DEFAULT '',
    unit_price       bigint NOT NULL CHECK (unit_price >= 0),
    currency         text NOT NULL,
    updated_by       text NOT NULL,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, local_code)
);
