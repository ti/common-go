-- Token bucket (GCRA) for rate limiting, run atomically in Redis.
--
-- This file is the single source of the script: every backend (Go, Python,
-- Rust) loads it verbatim, so that services sharing one Redis compute the
-- same thing. The same arithmetic in Go is ratelimit.GCRA; both are pinned by
-- testdata/gcra_cases.json.
--
-- The bucket is one key holding the "theoretical arrival time" (TAT): the
-- time, in microseconds since the Unix epoch on the Redis server's clock
-- (replicas with skewed clocks agree), at which the bucket is full again.
-- The key expires at that time; a missing key is a full bucket.
--
--   KEYS[1]  bucket key:  <prefix>:<rule>|<subject|ip>|<value>[|<method>]#<interval>:<burst>
--            (an ip value is an IPv4 address or an IPv6 network such as
--            2001:db8:1:2::/64; a subject value is u:<user id> or c:<client id>;
--            <interval> and <burst> are ARGV[1] and ARGV[2] in decimal)
--            A key MUST be used with one interval and burst only, which is why
--            both are part of it: the stored value means nothing under another
--            limit, and the clock-step correction below would then hand out
--            tokens. A changed limit starts fresh buckets.
--   ARGV[1]  interval: microseconds one token takes to refill (period / rate), >= 1
--   ARGV[2]  burst: bucket size in tokens, >= 1
--   ARGV[3]  n: tokens to take, >= 1
--
-- Returns four integers:
--   [1] allowed      1 when the tokens were taken, 0 when not (nothing is consumed)
--   [2] remaining    tokens left in the bucket
--   [3] retry_after  microseconds until the request would be allowed; 0 when allowed
--   [4] reset_after  microseconds until the bucket is full again
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local n = tonumber(ARGV[3])
local tolerance = interval * burst
local stored = tonumber(redis.call('GET', KEYS[1]))
local tat = stored
if not tat or tat < now then
  tat = now
end
-- A stored TAT further ahead than an empty bucket means the clock stepped
-- backwards since it was written (failover to a server with another clock):
-- treat the bucket as empty instead of locking the key out for the step.
if tat > now + tolerance then
  tat = now + tolerance
end
local nxt = tat + interval * n
if nxt - now > tolerance then
  if stored and stored > tat then
    -- Keep the corrected value, or the key stays locked out until the clock
    -- has caught up with the old one.
    redis.call('SET', KEYS[1], string.format('%.0f', tat), 'PX', math.max(1, math.ceil((tat - now) / 1000)))
  end
  return {0, math.floor((tolerance - (tat - now)) / interval), nxt - tolerance - now, tat - now}
end
redis.call('SET', KEYS[1], string.format('%.0f', nxt), 'PX', math.max(1, math.ceil((nxt - now) / 1000)))
return {1, math.floor((tolerance - (nxt - now)) / interval), 0, nxt - now}
