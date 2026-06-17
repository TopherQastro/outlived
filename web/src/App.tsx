import React, { useCallback, useEffect, useState } from 'react'
import ReactDOM from 'react-dom'

import {
    AppBar,
    Box,
    Card,
    CardContent,
    CardHeader,
    Checkbox,
    CircularProgress,
    FormControl,
    FormControlLabel,
    InputLabel,
    Link,
    MenuItem,
    Select,
    Snackbar,
    ThemeProvider,
    Typography,
} from '@material-ui/core'
import { Alert } from '@material-ui/lab'
import {
    createTheme,
    makeStyles,
    Theme,
    useTheme,
} from '@material-ui/core/styles'

import { Figures } from './Figures'
import { TopBar } from './TopBar'
import { User } from './User'

import { post } from './post'
import { Data, FigureData, UserData } from './types'
import { tzname } from './tz'
import { daysInMonth } from './util'

// https://paletton.com/#uid=23+0u0k87oa2jC650sPbokcd+eW
const theme = createTheme({
    palette: {
        primary: {
            light: '#B8B8C3',
            main: '#8B8BA1',
            dark: '#6A6A87',
            contrastText: '#343453',
        },
        secondary: {
            light: '#FFFAED',
            main: '#E6DDC2',
            dark: '#C1B490',
            contrastText: '#776A43',
        },
    },
    typography: {
        button: {
            textTransform: 'none',
        },
    },
})

const useStyles = makeStyles((theme: Theme) => ({
    today: {
        backgroundColor: theme.palette.primary.light,
        borderWidth: '4px',
        color: theme.palette.primary.dark,
        fontSize: '1.2rem',
        margin: '1rem',
        textAlign: 'center',
        width: 'fit-content',
    },
}))

// Age-range filter options. value is in days; 0 means no filter (all time).
const rangeOptions = [
    { label: 'Within 1 month', value: 30 },
    { label: 'Within 6 months', value: 182 },
    { label: 'Within 1 year', value: 365 },
    { label: 'Within 5 years', value: 1826 },
    { label: 'Within 10 years', value: 3652 },
    { label: 'All time', value: 0 },
]

// How many results to show.
const resultOptions = [24, 50, 100, 200]

export const App = () => {
    const [alert, setAlert] = useState('')
    const [alertSeverity, setAlertSeverity] = useState<'error' | 'info'>('error')
    const [figures, setFigures] = useState<FigureData[]>([])
    const [loaded, setLoaded] = useState(false)
    const [today, setToday] = useState('')
    const [user, setUser] = useState<UserData | null>(null)

    // Controls
    const [byPopularity, setByPopularity] = useState(false)
    const [rangeDays, setRangeDays] = useState(365)
    const [resultLimit, setResultLimit] = useState(24)

    const classes = useStyles()

    const getData = async () => {
        try {
            const resp = await post('/s/data', {
                tzname: tzname(),
                byPopularity,
                rangeDays,
                resultLimit,
            })
            const data = (await resp.json()) as Data

            // Allow the page to render even if no one famous died today.
            // Only treat it as a real error if we got neither died-today
            // figures NOR user/outlived data.
            if (!data.figures && !data.user) {
                setAlert('Error: received no figures from server')
            } else {
                setFigures(data.figures || [])
                setToday(data.today)
                setUser(data.user)
                setLoaded(true)
            }

            // Queue a refetch of the data for when the date changes.

            const now = new Date()
            let y = now.getFullYear()
            let m = 1 + now.getMonth()
            let d = now.getDate()

            d++
            if (d > daysInMonth(y, m)) {
                d = 1
                m++
            }
            if (m > 12) {
                m = 1
                y++
            }

            const tomw = new Date(y, m - 1, d)

            window.setTimeout(
                getData,
                tomw.getTime() - now.getTime() + 1000 + Math.random() * 300000
            )
        } catch (error) {
            setAlert(`Error loading data: ${error.message}`)
        }
    }

    useEffect(() => {
        if (!loaded && !alert) {
            getData()
        }
    }, [loaded, alert, byPopularity, rangeDays, resultLimit])

    const setAlertAPI = (msg: string, severity?: 'error' | 'info') => {
        setAlertSeverity(severity || 'error')
        setAlert(msg)
    }

    return (
        <ThemeProvider theme={theme}>
            <TopBar user={user} setUser={setUser} setAlert={setAlertAPI} />
            <Snackbar open={!!alert} onClose={() => setAlert('')}>
                <Alert severity={alertSeverity}>{alert}</Alert>
            </Snackbar>
            {loaded ? (
                <>
                    <Box display='flex' justifyContent='center' m='auto'>
                        <Card className={classes.today} raised={true} variant='outlined'>
                            <CardContent>
                                <CardHeader title={`Today is ${today}`} />
                                {user ? (
                                    <>
                                        <Typography>
                                            You were born on {user.born}, which was{' '}
                                            {user.daysAlive.toLocaleString()} days ago
                                            <br />({user.yearsDaysAlive}).
                                        </Typography>
                                        <Box style={{ marginTop: '10px' }}>
                                            <FormControlLabel
                                                control={
                                                    <Checkbox
                                                        checked={byPopularity}
                                                        onChange={(e) => {
                                                            setByPopularity(e.target.checked)
                                                            setLoaded(false)
                                                        }}
                                                        color="primary"
                                                    />
                                                }
                                                label="Sort matches by fame"
                                            />
                                        </Box>
                                        <Box display="flex" justifyContent="center" style={{ marginTop: '10px', gap: '16px' }}>
                                            <FormControl variant="outlined" size="small" style={{ minWidth: 180 }}>
                                                <InputLabel id="range-label">Age range</InputLabel>
                                                <Select
                                                    labelId="range-label"
                                                    value={rangeDays}
                                                    onChange={(e) => {
                                                        setRangeDays(e.target.value as number)
                                                        setLoaded(false)
                                                    }}
                                                    label="Age range"
                                                >
                                                    {rangeOptions.map((opt) => (
                                                        <MenuItem key={opt.value} value={opt.value}>
                                                            {opt.label}
                                                        </MenuItem>
                                                    ))}
                                                </Select>
                                            </FormControl>
                                            <FormControl variant="outlined" size="small" style={{ minWidth: 120 }}>
                                                <InputLabel id="limit-label">Results</InputLabel>
                                                <Select
                                                    labelId="limit-label"
                                                    value={resultLimit}
                                                    onChange={(e) => {
                                                        setResultLimit(e.target.value as number)
                                                        setLoaded(false)
                                                    }}
                                                    label="Results"
                                                >
                                                    {resultOptions.map((n) => (
                                                        <MenuItem key={n} value={n}>
                                                            {n}
                                                        </MenuItem>
                                                    ))}
                                                </Select>
                                            </FormControl>
                                        </Box>
                                    </>
                                ) : (
                                    undefined
                                )}
                            </CardContent>
                        </Card>
                    </Box>
                    <Figures
                        diedToday={figures}
                        outlived={user ? user.figures : undefined}
                    />
                    <Box alignItems='center' justifyContent='center' textAlign='center'>
                        <Typography paragraph={true} variant='caption'>
                            Data supplied by{' '}
                            <Link
                                target='_blank'
                                rel='noopener noreferrer'
                                href='https://en.wikipedia.org/'
                            >
                                Wikipedia
                            </Link>
                            , the free encyclopedia.
                        </Typography>
                        <Typography paragraph={true} variant='caption'>
                            Some graphic design elements supplied by Suzanne Glickstein.
                            Thanks Suze!
                        </Typography>
                        <Typography paragraph={true} variant='caption'>
                            Curious about how this site works? Read the source at{' '}
                            <Link
                                target='_blank'
                                rel='noopener noreferrer'
                                href='https://github.com/bobg/outlived/'
                            >
                                github.com/bobg/outlived
                            </Link>
                            !
                        </Typography>
                    </Box>
                </>
            ) : (
                <Box display='flex' justifyContent='center' m='2em'>
                    <CircularProgress />
                </Box>
            )}
        </ThemeProvider>
    )
}

ReactDOM.render(<App />, document.getElementById('root'))
