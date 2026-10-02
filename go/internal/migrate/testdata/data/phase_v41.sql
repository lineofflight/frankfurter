INSERT INTO rates (date, base, quote, mid, bid, ask, provider) VALUES
('1999-01-04','EUR','AFN',5577.39,NULL,NULL,'BDI'),
('2002-10-04','EUR','AFN',4685.87,NULL,NULL,'BDI'),
('2002-10-04','EUR','USD',0.9865,NULL,NULL,'BDI'),
('2004-03-31','EUR','AFN',5806.4,NULL,NULL,'BDI'),
('2004-03-31','EUR','AFA',5806.4,NULL,NULL,'BDI'),
('2004-03-31','EUR','USD',1.2224,NULL,NULL,'BDI'),
('2004-04-01','EUR','AFN',58.52,NULL,NULL,'BDI'),
('2004-04-01','EUR','USD',1.232,NULL,NULL,'BDI'),
('2004-03-31','AFN','PLN',0.0789,NULL,NULL,'NBP'),
('2004-03-31','USD','PLN',3.9077,NULL,NULL,'NBP');
INSERT OR IGNORE INTO weekly_rates (bucket_date, provider, base, quote, rate)
SELECT date(strftime('%Y-%m-%d', strftime('%Y-01-01', date), '+' || (CAST(strftime('%W', date) AS integer) * 7) || ' days')), provider, base, quote, avg(rate)
FROM rates WHERE provider = 'BDI' AND date BETWEEN '1999-01-01' AND '2004-12-31' GROUP BY 1, provider, base, quote;
INSERT OR IGNORE INTO monthly_rates (bucket_date, provider, base, quote, rate)
SELECT strftime('%Y-%m-01', date), provider, base, quote, avg(rate)
FROM rates WHERE provider = 'BDI' AND date BETWEEN '1999-01-01' AND '2004-12-31' GROUP BY 1, provider, base, quote;
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
('BDI','AFN','1999-01-04','2004-04-01'),
('BDI','AFA','2004-03-31','2004-03-31');
INSERT INTO blended_rates (date, quote, rate) VALUES ('2004-03-31','AFN',3000.0);
INSERT INTO blended_weekly_rates (bucket_date, quote, rate)
SELECT DISTINCT bucket_date, 'AFN', 3000.0 FROM weekly_rates WHERE provider = 'BDI' AND bucket_date BETWEEN '1999-01-01' AND '2004-12-31';
INSERT INTO blended_weekly_rates (bucket_date, quote, rate) VALUES ('2004-05-03','AFN',58.0);
INSERT INTO blended_monthly_rates (bucket_date, quote, rate)
SELECT DISTINCT bucket_date, 'AFN', 3000.0 FROM monthly_rates WHERE provider = 'BDI' AND bucket_date BETWEEN '1999-01-01' AND '2004-12-31';
INSERT INTO blended_monthly_rates (bucket_date, quote, rate) VALUES ('2004-05-01','AFN',58.0);
