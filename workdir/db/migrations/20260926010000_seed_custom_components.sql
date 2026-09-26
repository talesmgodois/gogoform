-- migrate:up
-- Reusable components built on top of the /fake/* demo endpoints: one
-- select-data and one autocomplete-data field per collection.
INSERT INTO "custom_components" ("name", "field") VALUES
  ('Car (select)', '{
    "type": "select-data", "name": "car", "label": "Car", "required": false,
    "data_source": {"url": "/fake/cars", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('Car (autocomplete)', '{
    "type": "autocomplete-data", "name": "car", "label": "Car", "required": false,
    "data_source": {"url": "/fake/cars", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('Person (select)', '{
    "type": "select-data", "name": "person", "label": "Person", "required": false,
    "data_source": {"url": "/fake/people", "label_key": "name", "value_key": "email"}
  }'::jsonb),
  ('Person (autocomplete)', '{
    "type": "autocomplete-data", "name": "person", "label": "Person", "required": false,
    "data_source": {"url": "/fake/people", "label_key": "name", "value_key": "email", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('Anime (select)', '{
    "type": "select-data", "name": "anime", "label": "Anime", "required": false,
    "data_source": {"url": "/fake/animes", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('Anime (autocomplete)', '{
    "type": "autocomplete-data", "name": "anime", "label": "Anime", "required": false,
    "data_source": {"url": "/fake/animes", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('City (select)', '{
    "type": "select-data", "name": "city", "label": "City", "required": false,
    "data_source": {"url": "/fake/cities", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('City (autocomplete)', '{
    "type": "autocomplete-data", "name": "city", "label": "City", "required": false,
    "data_source": {"url": "/fake/cities", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb);

-- migrate:down
-- Match on name AND field together (not name alone) so a user-created
-- component that happens to share one of these names, but not its exact
-- field definition, survives the rollback.
DELETE FROM "custom_components" WHERE ("name", "field") IN (
  ('Car (select)', '{
    "type": "select-data", "name": "car", "label": "Car", "required": false,
    "data_source": {"url": "/fake/cars", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('Car (autocomplete)', '{
    "type": "autocomplete-data", "name": "car", "label": "Car", "required": false,
    "data_source": {"url": "/fake/cars", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('Person (select)', '{
    "type": "select-data", "name": "person", "label": "Person", "required": false,
    "data_source": {"url": "/fake/people", "label_key": "name", "value_key": "email"}
  }'::jsonb),
  ('Person (autocomplete)', '{
    "type": "autocomplete-data", "name": "person", "label": "Person", "required": false,
    "data_source": {"url": "/fake/people", "label_key": "name", "value_key": "email", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('Anime (select)', '{
    "type": "select-data", "name": "anime", "label": "Anime", "required": false,
    "data_source": {"url": "/fake/animes", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('Anime (autocomplete)', '{
    "type": "autocomplete-data", "name": "anime", "label": "Anime", "required": false,
    "data_source": {"url": "/fake/animes", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb),
  ('City (select)', '{
    "type": "select-data", "name": "city", "label": "City", "required": false,
    "data_source": {"url": "/fake/cities", "label_key": "name", "value_key": "id"}
  }'::jsonb),
  ('City (autocomplete)', '{
    "type": "autocomplete-data", "name": "city", "label": "City", "required": false,
    "data_source": {"url": "/fake/cities", "label_key": "name", "value_key": "id", "search_param": "name"},
    "min_chars": 2
  }'::jsonb)
);
