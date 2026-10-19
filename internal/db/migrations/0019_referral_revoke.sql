-- A referral bonus is taken back once when the friend's payment it came from is fully refunded.
ALTER TABLE referral_bonuses ADD COLUMN revoked_at timestamptz;
