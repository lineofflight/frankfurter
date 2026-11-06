INSERT INTO rates (date, base, quote, mid, bid, ask, provider) VALUES
('2021-08-30','SLL','GMD',0.01,NULL,NULL,'CBG'),
('2022-07-12','SLL','GMD',4.11,NULL,NULL,'CBG'),
('2004-12-28','TRL','UZS',0.00078,NULL,NULL,'CBU'),
('2005-01-04','TRL','UZS',787.09,NULL,NULL,'CBU'),
('2009-02-17','TRL','UZS',849.84,NULL,NULL,'CBU'),
('2009-02-17','TRY','UZS',849.84,NULL,NULL,'CBU'),
('1999-12-31','BYR','LTL',0.000004444,NULL,NULL,'LB'),
('2000-01-03','BYR','LTL',0.0044444,NULL,NULL,'LB'),
('1997-12-31','RUR','UAH',0.000319,NULL,NULL,'NBU'),
('1999-01-04','RUR','UAH',0.16596,NULL,NULL,'NBU'),
('2004-03-31','RUR','UAH',0.18709,NULL,NULL,'NBU'),
('2004-03-31','RUB','UAH',0.18709,NULL,NULL,'NBU'),
('1999-07-01','BGL','UAH',0.0021073,NULL,NULL,'NBU'),
('1999-08-02','BGL','UAH',2.2594804,NULL,NULL,'NBU'),
('2000-01-03','BGL','UAH',0.2712867,NULL,NULL,'NBU'),
('2005-01-05','TRL','UAH',0.00000376,NULL,NULL,'NBU'),
('2005-06-27','TRL','UAH',0.03729741,NULL,NULL,'NBU'),
('2005-06-27','USD','UAH',5.055,NULL,NULL,'NBU'),
('2005-07-01','ROL','UAH',0.01690657,NULL,NULL,'NBU'),
('2006-01-06','AZM','UAH',0.05498693,NULL,NULL,'NBU'),
('2009-01-06','TMM','UAH',0.02701754,NULL,NULL,'NBU'),
('2014-04-22','MZM','AOA',3.1,NULL,NULL,'BNA'),
('2023-02-17','STD','AOA',0.02398,NULL,NULL,'BNA'),
('2023-02-22','STD','AOA',21.8914,NULL,NULL,'BNA'),
('2023-10-18','VEF','AOA',23.74128,NULL,NULL,'BNA'),
('2026-02-05','VEF','AOA',2.48819,NULL,NULL,'BNA'),
('2026-02-05','VES','AOA',2.487,NULL,NULL,'BNA'),
('2008-07-31','EUR','ZWD',108471581765.0,NULL,NULL,'BDI'),
('2008-08-01','EUR','ZWD',11.805092,NULL,NULL,'BDI'),
('2009-02-03','EUR','ZWD',28.2678,NULL,NULL,'BDI'),
('2009-02-04','ZWR','PLN',1.0e-08,NULL,NULL,'NBP'),
('2009-02-25','ZWR','PLN',0.043749,NULL,NULL,'NBP'),
('2018-01-02','MRO','MAD',NULL,2.6161,2.6318,'BAM'),
('2018-04-16','MRO','MAD',0.25864,NULL,NULL,'BAM'),
('2018-04-17','MRO','MAD',25.857,NULL,NULL,'BAM'),
('2018-04-18','MRO','MAD',NULL,25.8,25.9,'BAM'),
('2018-04-17','USD','MAD',9.1664,NULL,NULL,'BAM'),
('2025-06-21','ZMK','TZS',111.6619,NULL,NULL,'BOTA'),
('2016-06-25','BYR','KGS',0.003402,NULL,NULL,'NBKR'),
('2026-09-26','BYR','KGS',0.003402,NULL,NULL,'NBKR'),
('2026-09-26','BYN','KGS',28.8456,NULL,NULL,'NBKR');
INSERT OR IGNORE INTO weekly_rates (bucket_date, provider, base, quote, rate)
SELECT date(strftime('%Y-%m-%d', strftime('%Y-01-01', date), '+' || (CAST(strftime('%W', date) AS integer) * 7) || ' days')), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('CBG','CBU','LB','NBU','BNA','BDI','NBP','BAM','BOTA','NBKR') AND date >= '2005-01-01' OR provider IN ('LB','NBU','CBU') GROUP BY 1, provider, base, quote;
INSERT OR IGNORE INTO monthly_rates (bucket_date, provider, base, quote, rate)
SELECT strftime('%Y-%m-01', date), provider, base, quote, avg(rate)
FROM rates WHERE provider IN ('CBG','CBU','LB','NBU','BNA','BDI','NBP','BAM','BOTA','NBKR') AND date >= '2005-01-01' OR provider IN ('LB','NBU','CBU') GROUP BY 1, provider, base, quote;
INSERT INTO weekly_rates (bucket_date, provider, base, quote, rate) VALUES ('2005-07-02','NBU','TRY','UAH',999);
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date) VALUES
('NBU','RUR','1997-12-31','2004-03-31'),
('NBU','TRL','2005-01-05','2005-06-27'),
('BNA','VEF','2023-10-18','2026-02-05'),
('NBKR','BYR','2016-06-25','2026-09-26');
INSERT INTO blended_rates (date, quote, rate) VALUES ('2005-06-27','TRL',135533.0),('2018-04-17','MRO',0.354);
INSERT INTO blended_weekly_rates (bucket_date, quote, rate) VALUES ('2005-07-02','TRL',135533.0);
INSERT INTO blended_monthly_rates (bucket_date, quote, rate) VALUES ('2018-04-01','MRO',0.354);
