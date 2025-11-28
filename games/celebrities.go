package main

// Each player provides their username, and the name of a celebrity or famous figure, alive or dead, fictional or real
// These are combined into a list, and provided to a moderator
// This list of celebrities (but not who provided each name) is read off by the moderator, or displayed briefly to all connected players
// Then, players take turns trying to guess which player provided each name
// If a guess is correct, the player whose character was guessed "loses", and joins the team of the guessing player
// Then, they get another chance to guess
// If a guess is incorrect, the turn passes to the next player

// Display formats:
// Tiles of each player name and celebrity name, dragged to match
// Two text fields, one for player name and one for celebrity name

// Implementation details:
// - Use websockets to send updates from all joined players to each new player(?)
// - Identify players by cookie on first connection(?)

// How to play
// - Each player joins, is assigned a cookie, and prompted for their name and a celebrity name
// - Alternatively, they can choose to be the moderator, if one does not already exist
// - Order player turns by who provided their celebrity name first
// - Information is provided in two columns: player names, and celebrity names (not ordered/sorted)