package residual

// Mixed has a function and a const, so the function chunks and no residual
// chunk is created for the const.

const MixedConst = 1

func Mixed() int { return MixedConst }
