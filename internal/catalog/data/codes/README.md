One file per payer, named `<payer id>.json`, mapping standard item ids to the
payer's own code:

    { "MED-0001": { "code": "PARA500", "description": "Paracetamol 500mg tab" } }

Loaded at start; an unknown item id fails the build's tests.
