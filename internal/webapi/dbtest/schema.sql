-- Test schema for the web-api Go port, derived from the final state of the
-- Django models in web_api/web_api/models.py (migrations 0001-0026).
-- Applied wholesale before each DB-backed test for a clean slate.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

DROP TABLE IF EXISTS account_membership CASCADE;
DROP TABLE IF EXISTS stripe_customer_information CASCADE;
DROP TABLE IF EXISTS pull_request_activity CASCADE;
DROP TABLE IF EXISTS pull_request_activity_progress CASCADE;
DROP TABLE IF EXISTS user_pull_request_activity CASCADE;
DROP TABLE IF EXISTS user_pull_request_activity_progress CASCADE;
DROP TABLE IF EXISTS github_event CASCADE;
DROP TABLE IF EXISTS account CASCADE;
DROP TABLE IF EXISTS "user" CASCADE;

CREATE TABLE "user" (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    github_id integer NOT NULL UNIQUE,
    github_login varchar(255) NOT NULL UNIQUE,
    github_access_token varchar(255) NOT NULL
);
CREATE INDEX user_created_at_idx ON "user" (created_at);

CREATE TABLE account (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    github_installation_id integer NOT NULL UNIQUE,
    github_account_id integer NOT NULL UNIQUE,
    github_account_login varchar(255) NOT NULL UNIQUE,
    github_account_type varchar(255) NOT NULL,
    trial_expiration timestamptz,
    trial_start timestamptz,
    trial_started_by_id uuid REFERENCES "user" (id) ON DELETE SET NULL,
    trial_email varchar(255) NOT NULL DEFAULT '',
    stripe_customer_id varchar(255) NOT NULL DEFAULT '',
    limit_billing_access_to_owners boolean NOT NULL DEFAULT FALSE,
    contact_emails text NOT NULL DEFAULT '',
    subscription_exempt boolean NOT NULL DEFAULT FALSE,
    subscription_exempt_message text,
    CONSTRAINT github_account_type_valid CHECK (github_account_type IN ('User', 'Organization')),
    CONSTRAINT contact_emails_max_length_2000 CHECK (char_length(contact_emails) < 2000)
);
CREATE INDEX account_created_at_idx ON account (created_at);
CREATE INDEX account_stripe_customer_id_idx ON account (stripe_customer_id);

CREATE TABLE account_membership (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    account_id uuid NOT NULL REFERENCES account (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    role varchar(255) NOT NULL,
    CONSTRAINT role_valid CHECK (role IN ('admin', 'member'))
);
CREATE INDEX account_membership_created_at_idx ON account_membership (created_at);

CREATE TABLE github_event (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    event_name varchar(255) NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX github_event_created_at_idx ON github_event (created_at);
CREATE INDEX github_event_event_name_idx ON github_event (event_name);
-- migration 0008_payload_installation_id_idx
CREATE INDEX payload_installation_id_idx ON github_event ((payload -> 'installation' ->> 'id'));

CREATE TABLE pull_request_activity (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    date date NOT NULL,
    total_opened integer NOT NULL,
    total_merged integer NOT NULL,
    total_closed integer NOT NULL,
    kodiak_approved integer NOT NULL,
    kodiak_merged integer NOT NULL,
    kodiak_updated integer NOT NULL,
    github_installation_id integer NOT NULL,
    CONSTRAINT unique_pull_request_activity UNIQUE (date, github_installation_id)
);
CREATE INDEX pull_request_activity_date_idx ON pull_request_activity (date);
CREATE INDEX pull_request_activity_installation_idx ON pull_request_activity (github_installation_id);

CREATE TABLE pull_request_activity_progress (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    min_date date NOT NULL
);

CREATE TABLE user_pull_request_activity (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    github_installation_id integer NOT NULL,
    github_repository_name varchar(255) NOT NULL,
    github_pull_request_number integer NOT NULL,
    github_user_login varchar(255) NOT NULL,
    github_user_id integer NOT NULL,
    is_private_repository boolean NOT NULL,
    opened_pull_request boolean NOT NULL,
    activity_date date NOT NULL,
    CONSTRAINT unique_user_pull_request_activity UNIQUE (
        github_installation_id, github_repository_name,
        github_pull_request_number, github_user_id, activity_date
    )
);
CREATE INDEX user_pull_request_activity_installation_idx ON user_pull_request_activity (github_installation_id);
CREATE INDEX user_pull_request_activity_date_idx ON user_pull_request_activity (activity_date);

CREATE TABLE user_pull_request_activity_progress (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    modified_at timestamptz NOT NULL DEFAULT NOW(),
    -- Django DateTimeField (unlike pull_request_activity_progress.min_date,
    -- which is a DateField)
    min_date timestamptz NOT NULL
);
CREATE INDEX user_pull_request_activity_progress_min_date_idx ON user_pull_request_activity_progress (min_date);

CREATE TABLE stripe_customer_information (
    id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    customer_id varchar(255) NOT NULL UNIQUE,
    subscription_id varchar(255) NOT NULL UNIQUE,
    plan_id varchar(255) NOT NULL,
    payment_method_id varchar(255) NOT NULL,
    customer_email varchar(255) NOT NULL,
    customer_balance bigint NOT NULL,
    customer_created bigint NOT NULL,
    customer_currency varchar(255),
    customer_name varchar(255),
    customer_address_line1 varchar(255),
    customer_address_city varchar(255),
    customer_address_country varchar(255),
    customer_address_line2 varchar(255),
    customer_address_postal_code varchar(255),
    customer_address_state varchar(255),
    payment_method_card_brand varchar(255),
    payment_method_card_exp_month varchar(255),
    payment_method_card_exp_year varchar(255),
    payment_method_card_last4 varchar(255),
    plan_amount bigint NOT NULL,
    plan_interval varchar(255),
    plan_interval_count bigint,
    subscription_quantity bigint NOT NULL,
    subscription_start_date bigint NOT NULL,
    subscription_current_period_end bigint NOT NULL,
    subscription_current_period_start bigint NOT NULL
);
