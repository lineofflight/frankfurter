INSERT INTO rates (date, base, quote, mid, bid, ask, provider) VALUES
('1996-06-03','XBA','MKD',50.2099,NULL,NULL,'NBRM'),
('1996-06-03','USD','MKD',40.781,NULL,NULL,'NBRM'),
('1998-12-31','XBA','MKD',60.9144,NULL,NULL,'NBRM'),
('1999-01-04','XBA','MKD',60.5994,NULL,NULL,'NBRM'),
('1999-01-04','EUR','MKD',60.5994,NULL,NULL,'NBRM'),
('1999-05-04','XBA','MKD',60.6199,NULL,NULL,'NBRM'),
('1999-05-04','EUR','MKD',60.6199,NULL,NULL,'NBRM'),
('1999-05-05','XBA','MKD',60.62,NULL,NULL,'NBRM'),
('1996-06-03','XEU','CZK',34.316,NULL,NULL,'CNB'),
('1996-06-03','XBA','BAM',1.97509972,NULL,NULL,'CBBH');
INSERT OR IGNORE INTO weekly_rates (bucket_date, provider, base, quote, rate)
SELECT date(strftime('%Y-%m-%d', strftime('%Y-01-01', date), '+' || (CAST(strftime('%W', date) AS integer) * 7) || ' days')), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('NBRM','CNB','CBBH') AND date BETWEEN '1996-01-01' AND '1999-12-31' GROUP BY 1, provider, base, quote;
INSERT OR IGNORE INTO monthly_rates (bucket_date, provider, base, quote, rate)
SELECT strftime('%Y-%m-01', date), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('NBRM','CNB','CBBH') AND date BETWEEN '1996-01-01' AND '1999-12-31' GROUP BY 1, provider, base, quote;
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
('NBRM','XBA','1996-06-03','1999-05-05'),
('CBBH','XBA','1996-06-03','1996-06-03');
INSERT INTO blended_rates (date, quote, rate) VALUES ('1996-06-03','XBA',0.8122);
INSERT INTO blended_monthly_rates (bucket_date, quote, rate) VALUES ('1999-05-01','XBA',0.67);
