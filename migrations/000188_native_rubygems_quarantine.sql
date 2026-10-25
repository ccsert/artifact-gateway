-- Independent retained RubyGems quarantine; existing publications remain readable.
ALTER TABLE native_rubygems_sessions ADD COLUMN quarantined boolean NOT NULL DEFAULT false;
