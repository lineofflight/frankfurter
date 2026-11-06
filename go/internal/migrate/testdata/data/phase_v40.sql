INSERT INTO rates (date, base, quote, mid, bid, ask, provider) VALUES
('2008-09-09','SDR','UZS',2048.52,NULL,NULL,'CBU'),
('2008-09-09','USD','UZS',1326.0,NULL,NULL,'CBU'),
('2008-09-16','SDR','UZS',2044.11,NULL,NULL,'CBU'),
('2008-09-16','XDR','UZS',2044.11,NULL,NULL,'CBU'),
('1999-07-01','MXM','TZS',0.0602,NULL,NULL,'BOTA'),
('1999-07-01','USD','TZS',740.0,NULL,NULL,'BOTA'),
('2002-02-26','BYB','PLN',0.002494,NULL,NULL,'NBP'),
('2002-12-24','AFA','PLN',0.000816,NULL,NULL,'NBP'),
('2003-01-07','AFA','PLN',0.089056,NULL,NULL,'NBP'),
('2003-01-07','USD','PLN',3.8582,NULL,NULL,'NBP'),
('2003-10-28','AON','PLN',0.0503,NULL,NULL,'NBP'),
('2000-01-03','BEL','ILS',1.0313,NULL,NULL,'BOI'),
('2000-01-03','ATS','ILS',NULL,3.0123,3.0345,'BOI'),
('2000-01-03','ESP','ILS',2.5004,NULL,NULL,'BOI'),
('2000-01-03','ITL','ILS',2.1486,NULL,NULL,'BOI'),
('2000-01-03','CBK_L','ILS',4.387,NULL,NULL,'BOI'),
('2000-01-03','USD','ILS',4.124,NULL,NULL,'BOI'),
('2000-01-04','BEL','ILS',1.0299,NULL,NULL,'BOI'),
('2000-01-04','BEF','ILS',0.10299,NULL,NULL,'BOI'),
('1999-07-02','EUR','BGL',1955.83,NULL,NULL,'BDI'),
('2001-06-01','EUR','BGL',1947.0,NULL,NULL,'BDI'),
('2001-06-01','EUR','BGN',1.947,NULL,NULL,'BDI');
INSERT OR IGNORE INTO weekly_rates (bucket_date, provider, base, quote, rate)
SELECT date(strftime('%Y-%m-%d', strftime('%Y-01-01', date), '+' || (CAST(strftime('%W', date) AS integer) * 7) || ' days')), provider, base, quote, avg(rate)
FROM rates WHERE date < '2010-01-01' GROUP BY 1, provider, base, quote;
INSERT OR IGNORE INTO monthly_rates (bucket_date, provider, base, quote, rate)
SELECT strftime('%Y-%m-01', date), provider, base, quote, avg(rate)
FROM rates WHERE date < '2010-01-01' GROUP BY 1, provider, base, quote;
INSERT INTO weekly_rates (bucket_date, provider, base, quote, rate) VALUES ('2008-08-25','CBU','SDR','UZS',999);
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
('BDI','BGL','1999-07-02','2001-06-01'),
('NBP','AFA','2002-12-24','2003-01-07');
INSERT INTO currency_exclusions (provider_key, iso_code, start_date, end_date) VALUES ('BOI','CBK_L','2000-01-03','2000-01-03');
INSERT INTO blended_rates (date, quote, rate) VALUES ('2008-09-09','GBP',0.8);
INSERT INTO blended_weekly_rates (bucket_date, quote, rate)
SELECT DISTINCT bucket_date, 'GBP', 0.8 FROM weekly_rates WHERE bucket_date < '2010-01-01';
INSERT INTO blended_weekly_rates (bucket_date, quote, rate) VALUES ('2020-01-06','GBP',0.8);
INSERT INTO blended_monthly_rates (bucket_date, quote, rate)
SELECT DISTINCT bucket_date, 'GBP', 0.8 FROM monthly_rates WHERE bucket_date < '2010-01-01';
INSERT INTO blended_monthly_rates (bucket_date, quote, rate) VALUES ('2020-01-01','GBP',0.8);
