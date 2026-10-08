-- +goose Up

-- The ending is one screen. The honorable mentions used to be a phase of
-- their own ahead of the podium, with a second press for the winner -- but
-- the standings are public all night, so the room already knows who won by
-- then, and going from the winner to the mentions and back to the winner
-- read as a loop. Now "End the night" lands on the podium, where the
-- mentions deal in and the plinths rise after them. `awards` leaves the
-- phase vocabulary; a game parked on it is finished.
UPDATE app_trivia_games SET phase = 'podium' WHERE phase = 'awards';

-- +goose Down
-- No way back: the rows are indistinguishable from games that finished.
