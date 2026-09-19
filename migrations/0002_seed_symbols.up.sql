-- Tradable instruments. pip_size drives client-side price formatting.
INSERT INTO symbols (code, display_name, category, pip_size) VALUES
    ('R_10',     'Volatility 10 Index',  'volatility', 3),
    ('R_25',     'Volatility 25 Index',  'volatility', 3),
    ('R_50',     'Volatility 50 Index',  'volatility', 4),
    ('R_75',     'Volatility 75 Index',  'volatility', 4),
    ('R_100',    'Volatility 100 Index', 'volatility', 2),
    ('BOOM500',  'Boom 500 Index',       'boom_crash', 4),
    ('BOOM1000', 'Boom 1000 Index',      'boom_crash', 4),
    ('CRASH500', 'Crash 500 Index',      'boom_crash', 4),
    ('CRASH1000','Crash 1000 Index',     'boom_crash', 4),
    ('EURUSD',   'EUR/USD',              'forex',      5),
    ('GBPUSD',   'GBP/USD',              'forex',      5),
    ('USDJPY',   'USD/JPY',              'forex',      3)
ON CONFLICT (code) DO NOTHING;
