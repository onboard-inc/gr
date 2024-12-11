module drozd.in/main

go 1.23

require (
	drozd.in/intra v1.0.0
	drozd.in/neighbour v1.0.0
)

replace (
	drozd.in/intra => ./intra
	drozd.in/neighbour => ../neighbour
)
