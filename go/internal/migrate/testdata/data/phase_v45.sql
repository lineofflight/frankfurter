INSERT INTO rates (date, base, quote, mid, bid, ask, provider) VALUES
('1994-12-29','PLN','LTL',0.0001638,NULL,NULL,'LB'),
('1994-12-30','PLN','LTL',0.000164,NULL,NULL,'LB'),
('1995-01-02','PLN','LTL',0.0001641,NULL,NULL,'LB'),
('1995-01-03','PLN','LTL',1.646,NULL,NULL,'LB'),
('1995-01-04','PLN','LTL',1.645,NULL,NULL,'LB'),
('1998-01-02','RUB','LTL',0.0006694,NULL,NULL,'LB'),
('1998-01-05','RUB','LTL',0.6672,NULL,NULL,'LB'),
('1999-07-06','BGN','LTL',0.0021467,NULL,NULL,'LB'),
('1999-07-07','BGN','LTL',2.0952,NULL,NULL,'LB'),
('2005-07-01','RON','LTL',0.000095538,NULL,NULL,'LB'),
('2005-07-04','RON','LTL',0.95814,NULL,NULL,'LB'),
('2006-07-07','MZN','LTL',0.00010536,NULL,NULL,'LB'),
('2006-07-10','MZN','LTL',0.10531,NULL,NULL,'LB'),
('2000-10-27','TJS','AMD',2.668,NULL,NULL,'CBA'),
('2000-10-30','TJS','AMD',2.671,NULL,NULL,'CBA'),
('2000-11-01','TJS','AMD',250.74,NULL,NULL,'CBA'),
('2004-12-29','KZT','AMD',NULL,37.3,37.42,'CBA'),
('2004-12-30','KZT','AMD',37.37,NULL,NULL,'CBA'),
('2004-12-30','AMD','KZT',0.02676,NULL,NULL,'CBA'),
('2005-01-04','KZT','AMD',3.739,NULL,NULL,'CBA');
INSERT OR IGNORE INTO weekly_rates (bucket_date, provider, base, quote, rate)
SELECT date(strftime('%Y-%m-%d', strftime('%Y-01-01', date), '+' || (CAST(strftime('%W', date) AS integer) * 7) || ' days')), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('LB','CBA') AND date BETWEEN '1994-01-01' AND '2006-12-31' GROUP BY 1, provider, base, quote;
INSERT OR IGNORE INTO monthly_rates (bucket_date, provider, base, quote, rate)
SELECT strftime('%Y-%m-01', date), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('LB','CBA') AND date BETWEEN '1994-01-01' AND '2006-12-31' GROUP BY 1, provider, base, quote;
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
('LB','PLN','1994-12-29','1995-01-04'),
('CBA','TJS','2000-10-27','2000-11-01'),
('CBA','KZT','2004-12-29','2005-01-04');
INSERT INTO rate_spikes (provider, date, base, quote) VALUES
('LB','1995-01-02','PLN','LTL'),
('CBA','2000-10-30','TJS','AMD');
INSERT INTO blended_rates (date, quote, rate) VALUES ('1995-01-02','PLN',24495.0);
